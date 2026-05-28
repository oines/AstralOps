package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	controlAttachmentMaxBytes         = 25 * 1024 * 1024
	controlAttachmentUploadMaxBytes   = 512 * 1024 * 1024
	controlAttachmentChunkMaxBytes    = 4 * 1024 * 1024
	controlAttachmentMetadata         = "attachment.json"
	controlAttachmentUploadMetadata   = "upload.json"
	controlAttachmentFileSubdir       = "file"
	controlAttachmentUploadPart       = "upload.part"
	controlAttachmentIDByteCount      = 18
	controlAttachmentUploadTTL        = 24 * time.Hour
	controlAttachmentNameMaxBytes     = 255
	controlAttachmentMIMETypeMaxBytes = 256
	controlAttachmentDetailMaxBytes   = 128
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

type attachmentIngestStartParams struct {
	SessionID string `json:"session_id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	MIMEType  string `json:"mime_type"`
	Detail    string `json:"detail"`
	Size      int64  `json:"size,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
}

type attachmentIngestStartResult struct {
	SessionID     string `json:"session_id"`
	UploadID      string `json:"upload_id"`
	AttachmentID  string `json:"attachment_id"`
	ChunkMaxBytes int64  `json:"chunk_max_bytes"`
	MaxBytes      int64  `json:"max_bytes"`
}

type attachmentIngestChunkParams struct {
	SessionID  string `json:"session_id"`
	UploadID   string `json:"upload_id"`
	Seq        int64  `json:"seq"`
	Offset     int64  `json:"offset"`
	DataBase64 string `json:"data_base64"`
}

type attachmentIngestChunkResult struct {
	SessionID     string `json:"session_id"`
	UploadID      string `json:"upload_id"`
	Seq           int64  `json:"seq"`
	Offset        int64  `json:"offset"`
	ReceivedBytes int64  `json:"received_bytes"`
}

type attachmentIngestFinishParams struct {
	SessionID string `json:"session_id"`
	UploadID  string `json:"upload_id"`
}

type attachmentIngestFinishResult struct {
	SessionID  string                  `json:"session_id"`
	UploadID   string                  `json:"upload_id"`
	Attachment controlAttachmentHandle `json:"attachment"`
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

type storedControlAttachmentUpload struct {
	SessionID      string `json:"session_id"`
	UploadID       string `json:"upload_id"`
	AttachmentID   string `json:"attachment_id"`
	Name           string `json:"name"`
	Kind           string `json:"kind"`
	MIMEType       string `json:"mime_type,omitempty"`
	Detail         string `json:"detail,omitempty"`
	ExpectedSize   int64  `json:"expected_size,omitempty"`
	ExpectedSHA256 string `json:"expected_sha256,omitempty"`
	ReceivedBytes  int64  `json:"received_bytes"`
	LastSeq        int64  `json:"last_seq"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

type controlAttachmentDescriptor struct {
	Name     string
	Kind     string
	MIMEType string
	Detail   string
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
	descriptor, err := controlAttachmentDescriptorFromParams(params.Name, params.Kind, params.MIMEType, params.Detail, body)
	if err != nil {
		return attachmentIngestResult{}, err
	}

	dir := a.controlAttachmentDir(sessionID, id)
	fileDir := filepath.Join(dir, controlAttachmentFileSubdir)
	if err := os.MkdirAll(fileDir, 0o700); err != nil {
		return attachmentIngestResult{}, newActionError(http.StatusInternalServerError, "attachment_store_failed", err.Error())
	}
	target := filepath.Join(fileDir, descriptor.Name)
	if err := os.WriteFile(target, body, 0o600); err != nil {
		return attachmentIngestResult{}, newActionError(http.StatusInternalServerError, "attachment_store_failed", err.Error())
	}

	attachment := InputAttachment{
		ID:       id,
		Kind:     descriptor.Kind,
		Path:     target,
		Name:     descriptor.Name,
		MIMEType: descriptor.MIMEType,
		Size:     int64(len(body)),
		Detail:   descriptor.Detail,
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

func (a *app) startControlAttachmentIngest(params attachmentIngestStartParams) (attachmentIngestStartResult, error) {
	sessionID := strings.TrimSpace(params.SessionID)
	if sessionID == "" {
		return attachmentIngestStartResult{}, newActionError(http.StatusBadRequest, "session_id_required", "session_id required")
	}
	if _, ok := a.store.getSession(sessionID); !ok {
		return attachmentIngestStartResult{}, newActionError(http.StatusNotFound, "session_not_found", "session not found")
	}
	if params.Size < 0 || params.Size > controlAttachmentUploadMaxBytes {
		return attachmentIngestStartResult{}, newActionError(http.StatusRequestEntityTooLarge, "attachment_too_large", "attachment is too large")
	}
	expectedSHA := strings.ToLower(strings.TrimSpace(params.SHA256))
	if expectedSHA != "" {
		if _, err := hex.DecodeString(expectedSHA); err != nil || len(expectedSHA) != sha256.Size*2 {
			return attachmentIngestStartResult{}, newActionError(http.StatusBadRequest, "attachment_sha256_invalid", "attachment sha256 is invalid")
		}
	}

	id := "att_" + randomID(controlAttachmentIDByteCount)
	descriptor, err := controlAttachmentDescriptorFromParams(params.Name, params.Kind, params.MIMEType, params.Detail, nil)
	if err != nil {
		return attachmentIngestStartResult{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	upload := storedControlAttachmentUpload{
		SessionID:      sessionID,
		UploadID:       id,
		AttachmentID:   id,
		Name:           descriptor.Name,
		Kind:           descriptor.Kind,
		MIMEType:       descriptor.MIMEType,
		Detail:         descriptor.Detail,
		ExpectedSize:   params.Size,
		ExpectedSHA256: expectedSHA,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := os.MkdirAll(a.controlAttachmentDir(sessionID, id), 0o700); err != nil {
		return attachmentIngestStartResult{}, newActionError(http.StatusInternalServerError, "attachment_store_failed", err.Error())
	}
	if err := a.writeControlAttachmentUpload(upload); err != nil {
		return attachmentIngestStartResult{}, newActionError(http.StatusInternalServerError, "attachment_store_failed", err.Error())
	}
	return attachmentIngestStartResult{
		SessionID:     sessionID,
		UploadID:      id,
		AttachmentID:  id,
		ChunkMaxBytes: controlAttachmentChunkMaxBytes,
		MaxBytes:      controlAttachmentUploadMaxBytes,
	}, nil
}

func (a *app) appendControlAttachmentChunk(params attachmentIngestChunkParams) (attachmentIngestChunkResult, error) {
	upload, err := a.loadControlAttachmentUpload(params.SessionID, params.UploadID)
	if err != nil {
		return attachmentIngestChunkResult{}, err
	}
	if params.Seq <= 0 || params.Seq != upload.LastSeq+1 {
		return attachmentIngestChunkResult{}, newActionError(http.StatusBadRequest, "attachment_chunk_seq_invalid", "attachment chunk seq is invalid")
	}
	if params.Offset != upload.ReceivedBytes {
		return attachmentIngestChunkResult{}, newActionError(http.StatusBadRequest, "attachment_chunk_offset_invalid", "attachment chunk offset is invalid")
	}
	encoded := strings.TrimSpace(params.DataBase64)
	if len(encoded) > base64.StdEncoding.EncodedLen(controlAttachmentChunkMaxBytes) {
		return attachmentIngestChunkResult{}, newActionError(http.StatusRequestEntityTooLarge, "attachment_chunk_too_large", "attachment chunk is too large")
	}
	body, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return attachmentIngestChunkResult{}, newActionError(http.StatusBadRequest, "attachment_chunk_invalid", "attachment chunk data_base64 is invalid")
	}
	if len(body) == 0 {
		return attachmentIngestChunkResult{}, newActionError(http.StatusBadRequest, "attachment_chunk_empty", "attachment chunk is empty")
	}
	if len(body) > controlAttachmentChunkMaxBytes {
		return attachmentIngestChunkResult{}, newActionError(http.StatusRequestEntityTooLarge, "attachment_chunk_too_large", "attachment chunk is too large")
	}
	nextSize := upload.ReceivedBytes + int64(len(body))
	if nextSize > controlAttachmentUploadMaxBytes || upload.ExpectedSize > 0 && nextSize > upload.ExpectedSize {
		return attachmentIngestChunkResult{}, newActionError(http.StatusRequestEntityTooLarge, "attachment_too_large", "attachment is too large")
	}
	partPath := a.controlAttachmentUploadPartPath(upload.SessionID, upload.UploadID)
	file, err := os.OpenFile(partPath, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return attachmentIngestChunkResult{}, newActionError(http.StatusInternalServerError, "attachment_store_failed", err.Error())
	}
	if _, err := file.Seek(upload.ReceivedBytes, io.SeekStart); err != nil {
		file.Close()
		return attachmentIngestChunkResult{}, newActionError(http.StatusInternalServerError, "attachment_store_failed", err.Error())
	}
	if _, err := file.Write(body); err != nil {
		file.Close()
		return attachmentIngestChunkResult{}, newActionError(http.StatusInternalServerError, "attachment_store_failed", err.Error())
	}
	if err := file.Close(); err != nil {
		return attachmentIngestChunkResult{}, newActionError(http.StatusInternalServerError, "attachment_store_failed", err.Error())
	}
	upload.LastSeq = params.Seq
	upload.ReceivedBytes = nextSize
	upload.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := a.writeControlAttachmentUpload(upload); err != nil {
		return attachmentIngestChunkResult{}, newActionError(http.StatusInternalServerError, "attachment_store_failed", err.Error())
	}
	return attachmentIngestChunkResult{
		SessionID:     upload.SessionID,
		UploadID:      upload.UploadID,
		Seq:           upload.LastSeq,
		Offset:        params.Offset,
		ReceivedBytes: upload.ReceivedBytes,
	}, nil
}

func (a *app) finishControlAttachmentIngest(params attachmentIngestFinishParams) (attachmentIngestFinishResult, error) {
	upload, err := a.loadControlAttachmentUpload(params.SessionID, params.UploadID)
	if err != nil {
		return attachmentIngestFinishResult{}, err
	}
	if upload.ExpectedSize > 0 && upload.ReceivedBytes != upload.ExpectedSize {
		return attachmentIngestFinishResult{}, newActionError(http.StatusBadRequest, "attachment_size_mismatch", "attachment size does not match expected size")
	}
	partPath := a.controlAttachmentUploadPartPath(upload.SessionID, upload.UploadID)
	info, err := os.Stat(partPath)
	if os.IsNotExist(err) && upload.ReceivedBytes == 0 {
		if writeErr := os.WriteFile(partPath, nil, 0o600); writeErr != nil {
			return attachmentIngestFinishResult{}, newActionError(http.StatusInternalServerError, "attachment_store_failed", writeErr.Error())
		}
		info, err = os.Stat(partPath)
	}
	if err != nil || info.IsDir() {
		return attachmentIngestFinishResult{}, newActionError(http.StatusNotFound, "attachment_upload_not_found", "attachment upload not found")
	}
	if info.Size() != upload.ReceivedBytes {
		return attachmentIngestFinishResult{}, newActionError(http.StatusBadRequest, "attachment_size_mismatch", "attachment upload size does not match metadata")
	}
	if upload.ExpectedSHA256 != "" {
		actual, err := fileSHA256Hex(partPath)
		if err != nil {
			return attachmentIngestFinishResult{}, newActionError(http.StatusInternalServerError, "attachment_hash_failed", err.Error())
		}
		if actual != upload.ExpectedSHA256 {
			return attachmentIngestFinishResult{}, newActionError(http.StatusBadRequest, "attachment_sha256_mismatch", "attachment sha256 does not match")
		}
	}
	fileDir := filepath.Join(a.controlAttachmentDir(upload.SessionID, upload.AttachmentID), controlAttachmentFileSubdir)
	if err := os.MkdirAll(fileDir, 0o700); err != nil {
		return attachmentIngestFinishResult{}, newActionError(http.StatusInternalServerError, "attachment_store_failed", err.Error())
	}
	target := filepath.Join(fileDir, upload.Name)
	if err := os.Rename(partPath, target); err != nil {
		return attachmentIngestFinishResult{}, newActionError(http.StatusInternalServerError, "attachment_store_failed", err.Error())
	}
	attachment := InputAttachment{
		ID:       upload.AttachmentID,
		Kind:     upload.Kind,
		Path:     target,
		Name:     upload.Name,
		MIMEType: upload.MIMEType,
		Size:     upload.ReceivedBytes,
		Detail:   upload.Detail,
	}
	record := storedControlAttachment{
		SessionID:  upload.SessionID,
		Attachment: attachment,
		CreatedAt:  time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := writeJSONFile(filepath.Join(a.controlAttachmentDir(upload.SessionID, upload.AttachmentID), controlAttachmentMetadata), record, 0o600); err != nil {
		return attachmentIngestFinishResult{}, newActionError(http.StatusInternalServerError, "attachment_store_failed", err.Error())
	}
	_ = os.Remove(a.controlAttachmentUploadMetadataPath(upload.SessionID, upload.UploadID))
	return attachmentIngestFinishResult{
		SessionID:  upload.SessionID,
		UploadID:   upload.UploadID,
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

func (a *app) controlAttachmentUploadMetadataPath(sessionID, uploadID string) string {
	return filepath.Join(a.controlAttachmentDir(sessionID, uploadID), controlAttachmentUploadMetadata)
}

func (a *app) controlAttachmentUploadPartPath(sessionID, uploadID string) string {
	return filepath.Join(a.controlAttachmentDir(sessionID, uploadID), controlAttachmentUploadPart)
}

func (a *app) loadControlAttachmentUpload(sessionID, uploadID string) (storedControlAttachmentUpload, error) {
	sessionID = strings.TrimSpace(sessionID)
	uploadID = strings.TrimSpace(uploadID)
	if sessionID == "" || uploadID == "" {
		return storedControlAttachmentUpload{}, newActionError(http.StatusBadRequest, "attachment_upload_reference_invalid", "attachment upload reference is invalid")
	}
	body, err := os.ReadFile(a.controlAttachmentUploadMetadataPath(sessionID, uploadID))
	if err != nil {
		if os.IsNotExist(err) {
			return storedControlAttachmentUpload{}, newActionError(http.StatusNotFound, "attachment_upload_not_found", "attachment upload not found")
		}
		return storedControlAttachmentUpload{}, newActionError(http.StatusInternalServerError, "attachment_upload_read_failed", err.Error())
	}
	var upload storedControlAttachmentUpload
	if err := json.Unmarshal(body, &upload); err != nil {
		return storedControlAttachmentUpload{}, newActionError(http.StatusInternalServerError, "attachment_upload_metadata_invalid", err.Error())
	}
	if upload.SessionID != sessionID || upload.UploadID != uploadID || upload.AttachmentID == "" {
		return storedControlAttachmentUpload{}, newActionError(http.StatusNotFound, "attachment_upload_not_found", "attachment upload not found")
	}
	if controlAttachmentUploadExpired(upload, time.Now().UTC()) {
		a.cleanupControlAttachmentUpload(sessionID, uploadID)
		return storedControlAttachmentUpload{}, newActionError(http.StatusGone, "attachment_upload_expired", "attachment upload expired")
	}
	return upload, nil
}

func (a *app) writeControlAttachmentUpload(upload storedControlAttachmentUpload) error {
	return writeJSONFile(a.controlAttachmentUploadMetadataPath(upload.SessionID, upload.UploadID), upload, 0o600)
}

func (a *app) cleanupControlAttachmentUpload(sessionID, uploadID string) {
	_ = os.Remove(a.controlAttachmentUploadMetadataPath(sessionID, uploadID))
	_ = os.Remove(a.controlAttachmentUploadPartPath(sessionID, uploadID))
	_ = os.Remove(a.controlAttachmentDir(sessionID, uploadID))
}

func controlAttachmentUploadExpired(upload storedControlAttachmentUpload, now time.Time) bool {
	updatedAt := strings.TrimSpace(upload.UpdatedAt)
	if updatedAt == "" {
		updatedAt = strings.TrimSpace(upload.CreatedAt)
	}
	if updatedAt == "" {
		return false
	}
	timestamp, err := time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return false
	}
	return now.Sub(timestamp) > controlAttachmentUploadTTL
}

func controlAttachmentDescriptorFromParams(name, kind, mimeType, detail string, body []byte) (controlAttachmentDescriptor, error) {
	safeName := safeAttachmentName(name)
	if len(safeName) > controlAttachmentNameMaxBytes {
		return controlAttachmentDescriptor{}, newActionError(http.StatusRequestEntityTooLarge, "attachment_metadata_too_large", "attachment name is too long")
	}
	explicitMIMEType := strings.TrimSpace(mimeType)
	if len(explicitMIMEType) > controlAttachmentMIMETypeMaxBytes {
		return controlAttachmentDescriptor{}, newActionError(http.StatusRequestEntityTooLarge, "attachment_metadata_too_large", "attachment mime_type is too long")
	}
	detail = strings.TrimSpace(detail)
	if len(detail) > controlAttachmentDetailMaxBytes {
		return controlAttachmentDescriptor{}, newActionError(http.StatusRequestEntityTooLarge, "attachment_metadata_too_large", "attachment detail is too long")
	}
	resolvedMIMEType := attachmentMIMEType(safeName, explicitMIMEType, body)
	resolvedKind := attachmentKind(strings.TrimSpace(kind), resolvedMIMEType)
	if detail == "" && resolvedKind == "image" {
		detail = "high"
	}
	return controlAttachmentDescriptor{
		Name:     safeName,
		Kind:     resolvedKind,
		MIMEType: resolvedMIMEType,
		Detail:   detail,
	}, nil
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

func fileSHA256Hex(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
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
