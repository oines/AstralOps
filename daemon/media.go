package main

import (
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func (a *app) handleSessionMedia(w http.ResponseWriter, r *http.Request, sessionID, seqText, mediaID string) {
	seq, err := strconv.ParseInt(seqText, 10, 64)
	if err != nil || seq <= 0 || strings.TrimSpace(mediaID) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid media reference"})
		return
	}
	events := a.store.queryEvents("", sessionID, 0)
	var target *AstralEvent
	for index := range events {
		if events[index].Seq == seq {
			target = &events[index]
			break
		}
	}
	if target == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "media event not found"})
		return
	}
	media, ok := mediaReferenceFromEvent(*target, mediaID)
	if !ok || media.Path == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "media not found"})
		return
	}
	info, err := os.Stat(media.Path)
	if err != nil || info.IsDir() {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "media file not found"})
		return
	}
	name := media.Name
	if strings.TrimSpace(name) == "" {
		name = filepath.Base(media.Path)
	}
	mimeType := media.MIMEType
	if mimeType == "" {
		mimeType = mime.TypeByExtension(strings.ToLower(filepath.Ext(name)))
	}
	if mimeType != "" {
		w.Header().Set("Content-Type", mimeType)
	}
	if r.URL.Query().Get("download") == "1" {
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	}
	http.ServeFile(w, r, media.Path)
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
