package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	controlAttachmentMaxBytes    = 25 * 1024 * 1024
	controlAttachmentMetadata    = "attachment.json"
	controlAttachmentFileSubdir  = "file"
	controlAttachmentIDByteCount = 18
)

type InputAttachment struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Path     string `json:"path"`
	Name     string `json:"name"`
	MIMEType string `json:"mime_type,omitempty"`
	Size     int64  `json:"size,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

type controlAttachmentHandle struct {
	ID        string `json:"id"`
	MediaID   string `json:"media_id"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	MIMEType  string `json:"mime_type,omitempty"`
	Size      int64  `json:"size,omitempty"`
	Detail    string `json:"detail,omitempty"`
	HostOwned bool   `json:"host_owned"`
}

type attachmentIngestParams struct {
	SessionID     string `json:"session_id"`
	Name          string `json:"name"`
	Kind          string `json:"kind"`
	MIMEType      string `json:"mime_type"`
	Detail        string `json:"detail"`
	ContentBase64 string `json:"content_base64"`
}

type attachmentIngestResult struct {
	SessionID  string                  `json:"session_id"`
	Attachment controlAttachmentHandle `json:"attachment"`
}

type storedControlAttachment struct {
	SessionID  string          `json:"session_id"`
	Attachment InputAttachment `json:"attachment"`
	CreatedAt  string          `json:"created_at"`
}

func sanitizeInputAttachments(attachments []InputAttachment) []InputAttachment {
	out := make([]InputAttachment, 0, len(attachments))
	for _, attachment := range attachments {
		attachment.ID = strings.TrimSpace(attachment.ID)
		attachment.Kind = strings.TrimSpace(attachment.Kind)
		attachment.Path = strings.TrimSpace(attachment.Path)
		attachment.Name = strings.TrimSpace(attachment.Name)
		attachment.MIMEType = strings.TrimSpace(attachment.MIMEType)
		attachment.Detail = strings.TrimSpace(attachment.Detail)
		if attachment.ID == "" || attachment.Path == "" {
			continue
		}
		if attachment.Kind != "image" {
			attachment.Kind = "file"
		}
		if attachment.Name == "" {
			attachment.Name = filepath.Base(attachment.Path)
		}
		if attachment.MIMEType == "" {
			attachment.MIMEType = mime.TypeByExtension(strings.ToLower(filepath.Ext(attachment.Name)))
		}
		if attachment.Size <= 0 {
			if info, err := os.Stat(attachment.Path); err == nil && !info.IsDir() {
				attachment.Size = info.Size()
			}
		}
		out = append(out, attachment)
	}
	return out
}

func (a *app) ingestControlAttachment(params attachmentIngestParams) (attachmentIngestResult, error) {
	sessionID := strings.TrimSpace(params.SessionID)
	if sessionID == "" {
		return attachmentIngestResult{}, newActionError(http.StatusBadRequest, "session_id_required", "session_id required")
	}
	if _, ok := a.store.getSession(sessionID); !ok {
		return attachmentIngestResult{}, newActionError(http.StatusNotFound, "session_not_found", "session not found")
	}

	encoded := strings.TrimSpace(params.ContentBase64)
	if len(encoded) > base64.StdEncoding.EncodedLen(controlAttachmentMaxBytes) {
		return attachmentIngestResult{}, newActionError(http.StatusRequestEntityTooLarge, "attachment_too_large", "attachment is too large")
	}
	body, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return attachmentIngestResult{}, newActionError(http.StatusBadRequest, "attachment_content_invalid", "attachment content_base64 is invalid")
	}
	if len(body) > controlAttachmentMaxBytes {
		return attachmentIngestResult{}, newActionError(http.StatusRequestEntityTooLarge, "attachment_too_large", "attachment is too large")
	}

	id := "att_" + randomID(controlAttachmentIDByteCount)
	name := safeAttachmentName(params.Name)
	mimeType := attachmentMIMEType(name, strings.TrimSpace(params.MIMEType), body)
	kind := attachmentKind(strings.TrimSpace(params.Kind), mimeType)
	detail := strings.TrimSpace(params.Detail)
	if detail == "" && kind == "image" {
		detail = "high"
	}

	dir := a.controlAttachmentDir(sessionID, id)
	fileDir := filepath.Join(dir, controlAttachmentFileSubdir)
	if err := os.MkdirAll(fileDir, 0o700); err != nil {
		return attachmentIngestResult{}, newActionError(http.StatusInternalServerError, "attachment_store_failed", err.Error())
	}
	target := filepath.Join(fileDir, name)
	if err := os.WriteFile(target, body, 0o600); err != nil {
		return attachmentIngestResult{}, newActionError(http.StatusInternalServerError, "attachment_store_failed", err.Error())
	}

	attachment := InputAttachment{
		ID:       id,
		Kind:     kind,
		Path:     target,
		Name:     name,
		MIMEType: mimeType,
		Size:     int64(len(body)),
		Detail:   detail,
	}
	record := storedControlAttachment{
		SessionID:  sessionID,
		Attachment: attachment,
		CreatedAt:  time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := writeJSONFile(filepath.Join(dir, controlAttachmentMetadata), record, 0o600); err != nil {
		return attachmentIngestResult{}, newActionError(http.StatusInternalServerError, "attachment_store_failed", err.Error())
	}
	return attachmentIngestResult{
		SessionID:  sessionID,
		Attachment: controlAttachmentHandleFromInput(attachment),
	}, nil
}

func (a *app) resolveControlInputAttachments(sessionID string, attachments []InputAttachment) ([]InputAttachment, error) {
	if len(attachments) == 0 {
		return nil, nil
	}
	out := make([]InputAttachment, 0, len(attachments))
	for _, attachment := range attachments {
		attachment.ID = strings.TrimSpace(attachment.ID)
		attachment.Path = strings.TrimSpace(attachment.Path)
		if attachment.Path != "" {
			return nil, newActionError(http.StatusBadRequest, "attachment_path_forbidden", "remote attachments must use Host-owned attachment handles")
		}
		if attachment.ID == "" {
			return nil, newActionError(http.StatusBadRequest, "attachment_id_required", "attachment id required")
		}
		stored, err := a.loadControlAttachment(sessionID, attachment.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, stored)
	}
	return out, nil
}

func (a *app) loadControlAttachment(sessionID, attachmentID string) (InputAttachment, error) {
	body, err := os.ReadFile(filepath.Join(a.controlAttachmentDir(sessionID, attachmentID), controlAttachmentMetadata))
	if err != nil {
		if os.IsNotExist(err) {
			return InputAttachment{}, newActionError(http.StatusNotFound, "attachment_not_found", "attachment not found")
		}
		return InputAttachment{}, newActionError(http.StatusInternalServerError, "attachment_read_failed", err.Error())
	}
	var record storedControlAttachment
	if err := json.Unmarshal(body, &record); err != nil {
		return InputAttachment{}, newActionError(http.StatusInternalServerError, "attachment_metadata_invalid", err.Error())
	}
	attachment := record.Attachment
	if record.SessionID != strings.TrimSpace(sessionID) || attachment.ID != strings.TrimSpace(attachmentID) || attachment.Path == "" {
		return InputAttachment{}, newActionError(http.StatusNotFound, "attachment_not_found", "attachment not found")
	}
	info, err := os.Stat(attachment.Path)
	if err != nil || info.IsDir() {
		return InputAttachment{}, newActionError(http.StatusNotFound, "attachment_file_not_found", "attachment file not found")
	}
	if attachment.Size <= 0 {
		attachment.Size = info.Size()
	}
	return attachment, nil
}

func (a *app) controlAttachmentDir(sessionID, attachmentID string) string {
	return filepath.Join(a.store.dataDir, "runtime", "uploads", safeControlPathSegment(sessionID), safeControlPathSegment(attachmentID))
}

func controlAttachmentHandleFromInput(attachment InputAttachment) controlAttachmentHandle {
	return controlAttachmentHandle{
		ID:        attachment.ID,
		MediaID:   attachment.ID,
		Kind:      attachment.Kind,
		Name:      attachment.Name,
		MIMEType:  attachment.MIMEType,
		Size:      attachment.Size,
		Detail:    attachment.Detail,
		HostOwned: true,
	}
}

func safeAttachmentName(name string) string {
	name = strings.TrimSpace(filepath.Base(name))
	if name == "." || name == ".." || name == string(filepath.Separator) || name == "" {
		return "attachment.bin"
	}
	name = strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', 0:
			return '_'
		default:
			return r
		}
	}, name)
	if name == "" {
		return "attachment.bin"
	}
	return name
}

func attachmentMIMEType(name, explicit string, body []byte) string {
	if explicit != "" {
		return explicit
	}
	if byExt := mime.TypeByExtension(strings.ToLower(filepath.Ext(name))); byExt != "" {
		return byExt
	}
	if len(body) > 0 {
		return http.DetectContentType(body)
	}
	return "application/octet-stream"
}

func attachmentKind(explicit, mimeType string) string {
	mimeType = strings.ToLower(strings.TrimSpace(strings.Split(mimeType, ";")[0]))
	if explicit == "image" && isNativeImageMIME(mimeType) {
		return "image"
	}
	if explicit == "" && isNativeImageMIME(mimeType) {
		return "image"
	}
	return "file"
}

func safeControlPathSegment(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	var b strings.Builder
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "unknown"
	}
	return b.String()
}

func transcriptAttachmentValues(attachments []InputAttachment) []map[string]any {
	out := make([]map[string]any, 0, len(attachments))
	for _, attachment := range attachments {
		value := map[string]any{
			"id":        attachment.ID,
			"media_id":  attachment.ID,
			"kind":      attachment.Kind,
			"path":      attachment.Path,
			"name":      attachment.Name,
			"mime_type": attachment.MIMEType,
		}
		if attachment.Size > 0 {
			value["size"] = attachment.Size
		}
		if attachment.Detail != "" {
			value["detail"] = attachment.Detail
		}
		out = append(out, value)
	}
	return out
}

func displayInputNormalized(input string, options TurnOptions) map[string]any {
	text := strings.TrimSpace(input)
	if display := strings.TrimSpace(options.DisplayInput); display != "" {
		text = display
	}
	normalized := map[string]any{"text": text}
	if len(options.Attachments) > 0 {
		normalized["attachments"] = transcriptAttachmentValues(options.Attachments)
	}
	return normalized
}

func inputWithAttachmentManifest(input string, attachments []InputAttachment) string {
	text := strings.TrimSpace(input)
	if len(attachments) == 0 {
		return text
	}
	var b strings.Builder
	if text != "" {
		b.WriteString(text)
		b.WriteString("\n\n")
	}
	b.WriteString("Attached files available to the agent:\n")
	for _, attachment := range attachments {
		kind := attachment.Kind
		if kind == "" {
			kind = "file"
		}
		name := attachment.Name
		if name == "" {
			name = filepath.Base(attachment.Path)
		}
		if attachment.MIMEType != "" {
			fmt.Fprintf(&b, "- [%s] %s (%s): %s\n", kind, name, attachment.MIMEType, attachment.Path)
		} else {
			fmt.Fprintf(&b, "- [%s] %s: %s\n", kind, name, attachment.Path)
		}
	}
	return strings.TrimSpace(b.String())
}

func attachmentAllowedDirs(attachments []InputAttachment) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, attachment := range attachments {
		if attachment.Path == "" {
			continue
		}
		dir := filepath.Clean(filepath.Dir(attachment.Path))
		if dir == "." || seen[dir] {
			continue
		}
		seen[dir] = true
		out = append(out, dir)
	}
	return out
}

func claudeImageContentBlocks(attachments []InputAttachment) []map[string]any {
	blocks := []map[string]any{}
	for _, attachment := range attachments {
		if !isNativeImageAttachment(attachment) || attachment.Path == "" {
			continue
		}
		body, err := os.ReadFile(attachment.Path)
		if err != nil {
			continue
		}
		mimeType := attachment.MIMEType
		if mimeType == "" {
			mimeType = http.DetectContentType(body)
		}
		blocks = append(blocks, map[string]any{
			"type": "image",
			"source": map[string]any{
				"type":       "base64",
				"media_type": mimeType,
				"data":       base64.StdEncoding.EncodeToString(body),
			},
		})
	}
	return blocks
}

func isNativeImageAttachment(attachment InputAttachment) bool {
	if attachment.Kind != "image" {
		return false
	}
	mimeType := strings.ToLower(strings.TrimSpace(strings.Split(attachment.MIMEType, ";")[0]))
	return isNativeImageMIME(mimeType)
}

func isNativeImageMIME(mimeType string) bool {
	switch mimeType {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return true
	default:
		return false
	}
}
