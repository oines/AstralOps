package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	controlMediaReadMaxBytes = 25 * 1024 * 1024
	mediaStreamFrameChunk    = "media.chunk"
	mediaStreamFrameComplete = "media.completed"
	mediaStreamFrameError    = "media.error"
	mediaStreamDefaultChunk  = 64 * 1024
	mediaStreamMaxChunk      = 256 * 1024
	mediaStreamTokenPrefix   = "media_stream_v1."
)

type resolvedSessionMedia struct {
	SessionID string
	EventSeq  int64
	MediaID   string
	Path      string
	Name      string
	Kind      string
	MIMEType  string
	Size      int64
}

type mediaReadParams struct {
	SessionID string `json:"session_id"`
	EventSeq  int64  `json:"event_seq"`
	MediaID   string `json:"media_id"`
}

type mediaStreamParams struct {
	SessionID   string `json:"session_id,omitempty"`
	EventSeq    int64  `json:"event_seq,omitempty"`
	MediaID     string `json:"media_id,omitempty"`
	ResumeToken string `json:"resume_token,omitempty"`
	Offset      int64  `json:"offset,omitempty"`
	ChunkSize   int    `json:"chunk_size,omitempty"`
}

type mediaStreamCancelParams struct {
	StreamID string `json:"stream_id"`
}

type mediaReadResult struct {
	SessionID     string `json:"session_id"`
	EventSeq      int64  `json:"event_seq"`
	MediaID       string `json:"media_id"`
	Kind          string `json:"kind"`
	Name          string `json:"name"`
	MIMEType      string `json:"mime_type,omitempty"`
	Size          int64  `json:"size,omitempty"`
	ContentBase64 string `json:"content_base64"`
	Download      bool   `json:"download,omitempty"`
}

type mediaStreamResult struct {
	StreamID    string `json:"stream_id"`
	ResumeToken string `json:"resume_token"`
	SessionID   string `json:"session_id"`
	EventSeq    int64  `json:"event_seq"`
	MediaID     string `json:"media_id"`
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	MIMEType    string `json:"mime_type,omitempty"`
	Size        int64  `json:"size,omitempty"`
	Offset      int64  `json:"offset"`
	ChunkSize   int    `json:"chunk_size"`
}

type mediaStreamCancelResult struct {
	StreamID  string `json:"stream_id"`
	Cancelled bool   `json:"cancelled"`
}

type mediaStreamFrame struct {
	StreamID     string `json:"stream_id"`
	ResumeToken  string `json:"resume_token,omitempty"`
	RequestID    string `json:"request_id,omitempty"`
	SessionID    string `json:"session_id"`
	EventSeq     int64  `json:"event_seq"`
	MediaID      string `json:"media_id"`
	Kind         string `json:"kind,omitempty"`
	Name         string `json:"name,omitempty"`
	MIMEType     string `json:"mime_type,omitempty"`
	Size         int64  `json:"size,omitempty"`
	Seq          int64  `json:"seq"`
	Offset       int64  `json:"offset"`
	DataBase64   string `json:"data_base64,omitempty"`
	Final        bool   `json:"final,omitempty"`
	ErrorCode    string `json:"error_code,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
}

type mediaStreamTokenPayload struct {
	SessionID string `json:"session_id"`
	EventSeq  int64  `json:"event_seq"`
	MediaID   string `json:"media_id"`
}

func (a *app) handleSessionMedia(w http.ResponseWriter, r *http.Request, sessionID, seqText, mediaID string) {
	seq, err := strconv.ParseInt(seqText, 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid media reference"})
		return
	}
	media, err := a.resolveSessionMedia(sessionID, seq, mediaID)
	if err != nil {
		writeActionError(w, err)
		return
	}
	if media.MIMEType != "" {
		w.Header().Set("Content-Type", media.MIMEType)
	}
	if r.URL.Query().Get("download") == "1" {
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", media.Name))
	}
	http.ServeFile(w, r, media.Path)
}

func (a *app) prepareControlMediaStream(params mediaStreamParams) (mediaStreamResult, error) {
	ref, err := mediaStreamReference(params)
	if err != nil {
		return mediaStreamResult{}, err
	}
	media, err := a.resolveSessionMedia(ref.SessionID, ref.EventSeq, ref.MediaID)
	if err != nil {
		return mediaStreamResult{}, err
	}
	offset := params.Offset
	if offset < 0 || offset > media.Size {
		return mediaStreamResult{}, newActionError(http.StatusBadRequest, "media_stream_offset_invalid", "media stream offset is invalid")
	}
	return mediaStreamResult{
		StreamID:    "media_" + randomID(16),
		ResumeToken: mediaStreamResumeToken(media.SessionID, media.EventSeq, media.MediaID),
		SessionID:   media.SessionID,
		EventSeq:    media.EventSeq,
		MediaID:     media.MediaID,
		Kind:        media.Kind,
		Name:        media.Name,
		MIMEType:    media.MIMEType,
		Size:        media.Size,
		Offset:      offset,
		ChunkSize:   mediaStreamChunkSize(params.ChunkSize),
	}, nil
}

func (a *app) streamControlMedia(ctx context.Context, result mediaStreamResult, conn *controlWSConn, requestID string) {
	media, err := a.resolveSessionMedia(result.SessionID, result.EventSeq, result.MediaID)
	if err != nil {
		conn.writePlain(controlPlainFrame{Type: mediaStreamFrameError, Media: mediaStreamErrorFrame(result, requestID, "media_not_found", err.Error())})
		return
	}
	file, err := os.Open(media.Path)
	if err != nil {
		conn.writePlain(controlPlainFrame{Type: mediaStreamFrameError, Media: mediaStreamErrorFrame(result, requestID, "media_file_not_found", "media file not found")})
		return
	}
	defer file.Close()
	offset := result.Offset
	if offset > 0 {
		if _, err := file.Seek(offset, io.SeekStart); err != nil {
			conn.writePlain(controlPlainFrame{Type: mediaStreamFrameError, Media: mediaStreamErrorFrame(result, requestID, "media_stream_seek_failed", err.Error())})
			return
		}
	}
	buffer := make([]byte, result.ChunkSize)
	seq := int64(0)
	for {
		if mediaStreamCancelled(ctx) {
			return
		}
		n, readErr := file.Read(buffer)
		if n > 0 {
			if mediaStreamCancelled(ctx) {
				return
			}
			seq++
			chunk := mediaStreamFrame{
				StreamID:    result.StreamID,
				ResumeToken: result.ResumeToken,
				RequestID:   requestID,
				SessionID:   result.SessionID,
				EventSeq:    result.EventSeq,
				MediaID:     result.MediaID,
				Kind:        result.Kind,
				Name:        result.Name,
				MIMEType:    result.MIMEType,
				Size:        result.Size,
				Seq:         seq,
				Offset:      offset,
				DataBase64:  base64.StdEncoding.EncodeToString(buffer[:n]),
			}
			conn.writePlain(controlPlainFrame{Type: mediaStreamFrameChunk, Media: &chunk})
			offset += int64(n)
		}
		if mediaStreamCancelled(ctx) {
			return
		}
		if readErr == nil {
			continue
		}
		if readErr == io.EOF {
			if offset < result.Size {
				conn.writePlain(controlPlainFrame{Type: mediaStreamFrameError, Media: mediaStreamOffsetErrorFrame(result, requestID, "media_stream_truncated", "media file changed during stream", offset)})
				return
			}
			conn.writePlain(controlPlainFrame{Type: mediaStreamFrameComplete, Media: &mediaStreamFrame{
				StreamID:    result.StreamID,
				ResumeToken: result.ResumeToken,
				RequestID:   requestID,
				SessionID:   result.SessionID,
				EventSeq:    result.EventSeq,
				MediaID:     result.MediaID,
				Kind:        result.Kind,
				Name:        result.Name,
				MIMEType:    result.MIMEType,
				Size:        result.Size,
				Seq:         seq + 1,
				Offset:      offset,
				Final:       true,
			}})
			return
		}
		conn.writePlain(controlPlainFrame{Type: mediaStreamFrameError, Media: mediaStreamErrorFrame(result, requestID, "media_stream_read_failed", readErr.Error())})
		return
	}
}

func mediaStreamCancelled(ctx context.Context) bool {
	return controlStreamCancelled(ctx)
}

func controlStreamCancelled(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return true
	default:
		return false
	}
}

func (a *app) readControlMedia(params mediaReadParams, download bool) (mediaReadResult, error) {
	media, err := a.resolveSessionMedia(params.SessionID, params.EventSeq, params.MediaID)
	if err != nil {
		return mediaReadResult{}, err
	}
	body, err := readControlMediaBytes(media)
	if err != nil {
		return mediaReadResult{}, err
	}
	return mediaReadResult{
		SessionID:     media.SessionID,
		EventSeq:      media.EventSeq,
		MediaID:       media.MediaID,
		Kind:          media.Kind,
		Name:          media.Name,
		MIMEType:      media.MIMEType,
		Size:          int64(len(body)),
		ContentBase64: base64.StdEncoding.EncodeToString(body),
		Download:      download,
	}, nil
}

func readControlMediaBytes(media resolvedSessionMedia) ([]byte, error) {
	if media.Size > controlMediaReadMaxBytes {
		return nil, newActionError(http.StatusRequestEntityTooLarge, "media_too_large", "media is too large for media.read; use media.stream")
	}
	file, err := os.Open(media.Path)
	if err != nil {
		return nil, newActionError(http.StatusNotFound, "media_file_not_found", "media file not found")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.IsDir() {
		return nil, newActionError(http.StatusNotFound, "media_file_not_found", "media file not found")
	}
	if info.Size() > controlMediaReadMaxBytes {
		return nil, newActionError(http.StatusRequestEntityTooLarge, "media_too_large", "media is too large for media.read; use media.stream")
	}
	body, err := io.ReadAll(io.LimitReader(file, controlMediaReadMaxBytes+1))
	if err != nil {
		return nil, newActionError(http.StatusBadRequest, "media_read_failed", err.Error())
	}
	if int64(len(body)) > controlMediaReadMaxBytes {
		return nil, newActionError(http.StatusRequestEntityTooLarge, "media_too_large", "media is too large for media.read; use media.stream")
	}
	return body, nil
}

func mediaStreamChunkSize(requested int) int {
	if requested <= 0 {
		return mediaStreamDefaultChunk
	}
	if requested > mediaStreamMaxChunk {
		return mediaStreamMaxChunk
	}
	return requested
}

func mediaStreamReference(params mediaStreamParams) (mediaReadParams, error) {
	token := strings.TrimSpace(params.ResumeToken)
	if token == "" {
		return mediaReadParams{
			SessionID: params.SessionID,
			EventSeq:  params.EventSeq,
			MediaID:   params.MediaID,
		}, nil
	}
	ref, err := decodeMediaStreamResumeToken(token)
	if err != nil {
		return mediaReadParams{}, err
	}
	if strings.TrimSpace(params.SessionID) != "" && strings.TrimSpace(params.SessionID) != ref.SessionID {
		return mediaReadParams{}, newActionError(http.StatusBadRequest, "media_stream_resume_token_mismatch", "resume token does not match session_id")
	}
	if params.EventSeq != 0 && params.EventSeq != ref.EventSeq {
		return mediaReadParams{}, newActionError(http.StatusBadRequest, "media_stream_resume_token_mismatch", "resume token does not match event_seq")
	}
	if strings.TrimSpace(params.MediaID) != "" && strings.TrimSpace(params.MediaID) != ref.MediaID {
		return mediaReadParams{}, newActionError(http.StatusBadRequest, "media_stream_resume_token_mismatch", "resume token does not match media_id")
	}
	return ref, nil
}

func mediaStreamResumeToken(sessionID string, eventSeq int64, mediaID string) string {
	body, err := json.Marshal(mediaStreamTokenPayload{
		SessionID: sessionID,
		EventSeq:  eventSeq,
		MediaID:   mediaID,
	})
	if err != nil {
		return ""
	}
	return mediaStreamTokenPrefix + base64.RawURLEncoding.EncodeToString(body)
}

func decodeMediaStreamResumeToken(token string) (mediaReadParams, error) {
	token = strings.TrimSpace(token)
	if !strings.HasPrefix(token, mediaStreamTokenPrefix) {
		return mediaReadParams{}, newActionError(http.StatusBadRequest, "media_stream_resume_token_invalid", "invalid media stream resume token")
	}
	body, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(token, mediaStreamTokenPrefix))
	if err != nil {
		return mediaReadParams{}, newActionError(http.StatusBadRequest, "media_stream_resume_token_invalid", "invalid media stream resume token")
	}
	var payload mediaStreamTokenPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return mediaReadParams{}, newActionError(http.StatusBadRequest, "media_stream_resume_token_invalid", "invalid media stream resume token")
	}
	if strings.TrimSpace(payload.SessionID) == "" || payload.EventSeq <= 0 || strings.TrimSpace(payload.MediaID) == "" {
		return mediaReadParams{}, newActionError(http.StatusBadRequest, "media_stream_resume_token_invalid", "invalid media stream resume token")
	}
	return mediaReadParams{
		SessionID: strings.TrimSpace(payload.SessionID),
		EventSeq:  payload.EventSeq,
		MediaID:   strings.TrimSpace(payload.MediaID),
	}, nil
}

func mediaStreamErrorFrame(result mediaStreamResult, requestID, code, message string) *mediaStreamFrame {
	return mediaStreamOffsetErrorFrame(result, requestID, code, message, result.Offset)
}

func mediaStreamOffsetErrorFrame(result mediaStreamResult, requestID, code, message string, offset int64) *mediaStreamFrame {
	return &mediaStreamFrame{
		StreamID:     result.StreamID,
		ResumeToken:  result.ResumeToken,
		RequestID:    requestID,
		SessionID:    result.SessionID,
		EventSeq:     result.EventSeq,
		MediaID:      result.MediaID,
		Kind:         result.Kind,
		Name:         result.Name,
		MIMEType:     result.MIMEType,
		Size:         result.Size,
		Offset:       offset,
		ErrorCode:    code,
		ErrorMessage: message,
	}
}

func (a *app) resolveSessionMedia(sessionID string, eventSeq int64, mediaID string) (resolvedSessionMedia, error) {
	sessionID = strings.TrimSpace(sessionID)
	mediaID = strings.TrimSpace(mediaID)
	if sessionID == "" || eventSeq <= 0 || mediaID == "" {
		return resolvedSessionMedia{}, newActionError(http.StatusBadRequest, "media_reference_invalid", "invalid media reference")
	}
	if _, ok := a.store.getSession(sessionID); !ok {
		return resolvedSessionMedia{}, newActionError(http.StatusNotFound, "session_not_found", "session not found")
	}
	events := a.store.queryEvents("", sessionID, 0)
	var target *AstralEvent
	for index := range events {
		if events[index].Seq == eventSeq {
			target = &events[index]
			break
		}
	}
	if target == nil {
		return resolvedSessionMedia{}, newActionError(http.StatusNotFound, "media_event_not_found", "media event not found")
	}
	media, ok := mediaReferenceFromEvent(*target, mediaID)
	if !ok || media.Path == "" {
		return resolvedSessionMedia{}, newActionError(http.StatusNotFound, "media_not_found", "media not found")
	}
	info, err := os.Stat(media.Path)
	if err != nil || info.IsDir() {
		return resolvedSessionMedia{}, newActionError(http.StatusNotFound, "media_file_not_found", "media file not found")
	}
	name := media.Name
	if strings.TrimSpace(name) == "" {
		name = filepath.Base(media.Path)
	}
	mimeType := media.MIMEType
	if mimeType == "" {
		mimeType = mime.TypeByExtension(strings.ToLower(filepath.Ext(name)))
	}
	size := media.Size
	if size <= 0 {
		size = info.Size()
	}
	kind := media.Kind
	if strings.TrimSpace(kind) == "" {
		kind = "file"
	}
	return resolvedSessionMedia{
		SessionID: sessionID,
		EventSeq:  eventSeq,
		MediaID:   mediaID,
		Path:      media.Path,
		Name:      name,
		Kind:      kind,
		MIMEType:  mimeType,
		Size:      size,
	}, nil
}

func mediaReferenceFromEvent(event AstralEvent, mediaID string) (InputAttachment, bool) {
	value := mapValue(event.Normalized)
	if event.Kind == "message.user" {
		for _, attachment := range attachmentsFromNormalized(value["attachments"]) {
			if attachment.ID == mediaID {
				return attachment, true
			}
		}
	}
	if event.Kind == "message.media" {
		attachment := attachmentFromNormalized(value)
		if attachment.ID == mediaID {
			return attachment, true
		}
	}
	return InputAttachment{}, false
}

func attachmentsFromNormalized(value any) []InputAttachment {
	switch raw := value.(type) {
	case []any:
		out := make([]InputAttachment, 0, len(raw))
		for _, item := range raw {
			attachment := attachmentFromNormalized(mapValue(item))
			if attachment.ID != "" {
				out = append(out, attachment)
			}
		}
		return out
	case []map[string]any:
		out := make([]InputAttachment, 0, len(raw))
		for _, item := range raw {
			attachment := attachmentFromNormalized(item)
			if attachment.ID != "" {
				out = append(out, attachment)
			}
		}
		return out
	default:
		return nil
	}
}

func attachmentFromNormalized(value map[string]any) InputAttachment {
	id := firstString(value["media_id"], value["id"], value["item_id"])
	path := firstString(value["path"], value["saved_path"], value["savedPath"])
	kind := firstString(value["kind"], value["type"])
	if kind != "image" {
		kind = "file"
	}
	size := int64(0)
	if rawSize, ok := intLikeValue(value["size"]); ok {
		size = int64(rawSize)
	}
	return InputAttachment{
		ID:       id,
		Kind:     kind,
		Path:     path,
		Name:     firstString(value["name"], filepath.Base(path)),
		MIMEType: firstString(value["mime_type"], value["mimeType"]),
		Size:     size,
		Detail:   firstString(value["detail"]),
	}
}
