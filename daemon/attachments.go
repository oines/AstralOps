package main

import (
	"encoding/base64"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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
	switch mimeType {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return true
	default:
		return false
	}
}
