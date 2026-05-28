package main

import (
	"encoding/base64"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

func (a *app) readControlMedia(params mediaReadParams, download bool) (mediaReadResult, error) {
	media, err := a.resolveSessionMedia(params.SessionID, params.EventSeq, params.MediaID)
	if err != nil {
		return mediaReadResult{}, err
	}
	body, err := os.ReadFile(media.Path)
	if err != nil {
		return mediaReadResult{}, newActionError(http.StatusNotFound, "media_file_not_found", "media file not found")
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
