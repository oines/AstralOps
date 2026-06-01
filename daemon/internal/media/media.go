package media

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/oines/astralops/daemon/internal/apperrors"
	"github.com/oines/astralops/pkg/protocol"
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

type ResolvedSessionMedia struct {
	SessionID string
	EventSeq  int64
	MediaID   string
	Path      string
	Name      string
	Kind      string
	MIMEType  string
	Size      int64
}

type mediaReadParams = protocol.MediaReadParams
type mediaStreamParams = protocol.MediaStreamParams
type mediaStreamCancelParams = protocol.MediaStreamCancelParams
type mediaReadResult = protocol.MediaReadResult
type mediaStreamResult = protocol.MediaStreamResult
type mediaStreamCancelResult = protocol.MediaStreamCancelResult
type mediaStreamFrame = protocol.MediaStreamFrame
type AstralEvent = protocol.AstralEvent

type mediaStreamTokenPayload struct {
	SessionID string `json:"session_id"`
	EventSeq  int64  `json:"event_seq"`
	MediaID   string `json:"media_id"`
}

func (s *Service) PrepareControlMediaStream(params mediaStreamParams) (mediaStreamResult, error) {
	ref, err := mediaStreamReference(params)
	if err != nil {
		return mediaStreamResult{}, err
	}
	media, err := s.ResolveSessionMedia(ref.SessionID, ref.EventSeq, ref.MediaID)
	if err != nil {
		return mediaStreamResult{}, err
	}
	offset := params.Offset
	if offset < 0 || offset > media.Size {
		return mediaStreamResult{}, apperrors.New(http.StatusBadRequest, "media_stream_offset_invalid", "media stream offset is invalid")
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

func (s *Service) StreamControlMedia(ctx context.Context, result mediaStreamResult, writer StreamWriter, requestID string) {
	media, err := s.ResolveSessionMedia(result.SessionID, result.EventSeq, result.MediaID)
	if err != nil {
		writer.WriteMedia(mediaStreamFrameError, mediaStreamErrorFrame(result, requestID, "media_not_found", err.Error()))
		return
	}
	file, err := os.Open(media.Path)
	if err != nil {
		writer.WriteMedia(mediaStreamFrameError, mediaStreamErrorFrame(result, requestID, "media_file_not_found", "media file not found"))
		return
	}
	defer file.Close()
	offset := result.Offset
	if offset > 0 {
		if _, err := file.Seek(offset, io.SeekStart); err != nil {
			writer.WriteMedia(mediaStreamFrameError, mediaStreamErrorFrame(result, requestID, "media_stream_seek_failed", err.Error()))
			return
		}
	}
	buffer := make([]byte, result.ChunkSize)
	seq := int64(0)
	for offset < result.Size {
		if mediaStreamCancelled(ctx) {
			return
		}
		readSize := streamReadSize(len(buffer), result.Size-offset)
		n, readErr := file.Read(buffer[:readSize])
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
			writer.WriteMedia(mediaStreamFrameChunk, &chunk)
			offset += int64(n)
		}
		if mediaStreamCancelled(ctx) {
			return
		}
		if readErr == nil {
			continue
		}
		if readErr == io.EOF {
			writer.WriteMedia(mediaStreamFrameError, mediaStreamOffsetErrorFrame(result, requestID, "media_stream_truncated", "media file changed during stream", offset))
			return
		}
		writer.WriteMedia(mediaStreamFrameError, mediaStreamErrorFrame(result, requestID, "media_stream_read_failed", readErr.Error()))
		return
	}
	if mediaStreamCancelled(ctx) {
		return
	}
	if info, err := os.Stat(media.Path); err != nil || info.IsDir() || info.Size() != result.Size {
		writer.WriteMedia(mediaStreamFrameError, mediaStreamOffsetErrorFrame(result, requestID, "media_stream_truncated", "media file changed during stream", offset))
		return
	}
	writer.WriteMedia(mediaStreamFrameComplete, &mediaStreamFrame{
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
	})
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

func streamReadSize(chunkSize int, remaining int64) int {
	if remaining <= 0 {
		return 0
	}
	if chunkSize <= 0 || int64(chunkSize) > remaining {
		return int(remaining)
	}
	return chunkSize
}

func (s *Service) ReadControlMedia(params mediaReadParams, download bool) (mediaReadResult, error) {
	media, err := s.ResolveSessionMedia(params.SessionID, params.EventSeq, params.MediaID)
	if err != nil {
		return mediaReadResult{}, err
	}
	body, err := ReadControlMediaBytes(media)
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

func ReadControlMediaBytes(media ResolvedSessionMedia) ([]byte, error) {
	if media.Size > controlMediaReadMaxBytes {
		return nil, apperrors.New(http.StatusRequestEntityTooLarge, "media_too_large", "media is too large for media.read; use media.stream")
	}
	file, err := os.Open(media.Path)
	if err != nil {
		return nil, apperrors.New(http.StatusNotFound, "media_file_not_found", "media file not found")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.IsDir() {
		return nil, apperrors.New(http.StatusNotFound, "media_file_not_found", "media file not found")
	}
	if info.Size() > controlMediaReadMaxBytes {
		return nil, apperrors.New(http.StatusRequestEntityTooLarge, "media_too_large", "media is too large for media.read; use media.stream")
	}
	body, err := io.ReadAll(io.LimitReader(file, controlMediaReadMaxBytes+1))
	if err != nil {
		return nil, apperrors.New(http.StatusBadRequest, "media_read_failed", err.Error())
	}
	if int64(len(body)) > controlMediaReadMaxBytes {
		return nil, apperrors.New(http.StatusRequestEntityTooLarge, "media_too_large", "media is too large for media.read; use media.stream")
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
		return mediaReadParams{}, apperrors.New(http.StatusBadRequest, "media_stream_resume_token_mismatch", "resume token does not match session_id")
	}
	if params.EventSeq != 0 && params.EventSeq != ref.EventSeq {
		return mediaReadParams{}, apperrors.New(http.StatusBadRequest, "media_stream_resume_token_mismatch", "resume token does not match event_seq")
	}
	if strings.TrimSpace(params.MediaID) != "" && strings.TrimSpace(params.MediaID) != ref.MediaID {
		return mediaReadParams{}, apperrors.New(http.StatusBadRequest, "media_stream_resume_token_mismatch", "resume token does not match media_id")
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
		return mediaReadParams{}, apperrors.New(http.StatusBadRequest, "media_stream_resume_token_invalid", "invalid media stream resume token")
	}
	body, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(token, mediaStreamTokenPrefix))
	if err != nil {
		return mediaReadParams{}, apperrors.New(http.StatusBadRequest, "media_stream_resume_token_invalid", "invalid media stream resume token")
	}
	var payload mediaStreamTokenPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return mediaReadParams{}, apperrors.New(http.StatusBadRequest, "media_stream_resume_token_invalid", "invalid media stream resume token")
	}
	if strings.TrimSpace(payload.SessionID) == "" || payload.EventSeq <= 0 || strings.TrimSpace(payload.MediaID) == "" {
		return mediaReadParams{}, apperrors.New(http.StatusBadRequest, "media_stream_resume_token_invalid", "invalid media stream resume token")
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

func (s *Service) ResolveSessionMedia(sessionID string, eventSeq int64, mediaID string) (ResolvedSessionMedia, error) {
	sessionID = strings.TrimSpace(sessionID)
	mediaID = strings.TrimSpace(mediaID)
	if sessionID == "" || eventSeq <= 0 || mediaID == "" {
		return ResolvedSessionMedia{}, apperrors.New(http.StatusBadRequest, "media_reference_invalid", "invalid media reference")
	}
	if _, ok := s.store.GetSession(sessionID); !ok {
		return ResolvedSessionMedia{}, apperrors.New(http.StatusNotFound, "session_not_found", "session not found")
	}
	events := s.store.QueryEvents("", sessionID, 0)
	var target *AstralEvent
	for index := range events {
		if events[index].Seq == eventSeq {
			target = &events[index]
			break
		}
	}
	if target == nil {
		return ResolvedSessionMedia{}, apperrors.New(http.StatusNotFound, "media_event_not_found", "media event not found")
	}
	media, ok := mediaReferenceFromEvent(*target, mediaID)
	if !ok || media.Path == "" {
		return ResolvedSessionMedia{}, apperrors.New(http.StatusNotFound, "media_not_found", "media not found")
	}
	info, err := os.Stat(media.Path)
	if err != nil || info.IsDir() {
		return ResolvedSessionMedia{}, apperrors.New(http.StatusNotFound, "media_file_not_found", "media file not found")
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
	return ResolvedSessionMedia{
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

const (
	ControlMediaReadMaxBytes = controlMediaReadMaxBytes
	StreamFrameChunk         = mediaStreamFrameChunk
	StreamFrameComplete      = mediaStreamFrameComplete
	StreamFrameError         = mediaStreamFrameError
)

func MediaStreamChunkSize(requested int) int {
	return mediaStreamChunkSize(requested)
}

func MediaStreamResumeToken(sessionID string, eventSeq int64, mediaID string) string {
	return mediaStreamResumeToken(sessionID, eventSeq, mediaID)
}

func AttachmentsFromNormalized(value any) []InputAttachment {
	return attachmentsFromNormalized(value)
}
