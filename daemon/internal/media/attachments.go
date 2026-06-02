package media

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/oines/astralops/daemon/internal/apperrors"
	"github.com/oines/astralops/pkg/protocol"
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

type InputAttachment = protocol.InputAttachment
type controlAttachmentHandle = protocol.ControlAttachmentHandle
type attachmentIngestParams = protocol.AttachmentIngestParams
type attachmentIngestStartParams = protocol.AttachmentIngestStartParams
type attachmentIngestStartResult = protocol.AttachmentIngestStartResult
type attachmentIngestChunkParams = protocol.AttachmentIngestChunkParams
type attachmentIngestChunkResult = protocol.AttachmentIngestChunkResult
type attachmentIngestFinishParams = protocol.AttachmentIngestFinishParams
type attachmentIngestFinishResult = protocol.AttachmentIngestFinishResult
type attachmentIngestResult = protocol.AttachmentIngestResult

type storedControlAttachment struct {
	SessionID  string          `json:"session_id"`
	Attachment InputAttachment `json:"attachment"`
	CreatedAt  string          `json:"created_at"`
}

type StoredControlAttachmentUpload struct {
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

func SanitizeInputAttachments(attachments []InputAttachment) []InputAttachment {
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

func (s *Service) IngestControlAttachment(params attachmentIngestParams) (attachmentIngestResult, error) {
	sessionID := strings.TrimSpace(params.SessionID)
	if sessionID == "" {
		return attachmentIngestResult{}, apperrors.New(http.StatusBadRequest, "session_id_required", "session_id required")
	}
	if _, ok := s.store.GetSession(sessionID); !ok {
		return attachmentIngestResult{}, apperrors.New(http.StatusNotFound, "session_not_found", "session not found")
	}

	encoded := strings.TrimSpace(params.ContentBase64)
	if len(encoded) > base64.StdEncoding.EncodedLen(controlAttachmentMaxBytes) {
		return attachmentIngestResult{}, apperrors.New(http.StatusRequestEntityTooLarge, "attachment_too_large", "attachment is too large")
	}
	body, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return attachmentIngestResult{}, apperrors.New(http.StatusBadRequest, "attachment_content_invalid", "attachment content_base64 is invalid")
	}
	if len(body) > controlAttachmentMaxBytes {
		return attachmentIngestResult{}, apperrors.New(http.StatusRequestEntityTooLarge, "attachment_too_large", "attachment is too large")
	}

	id := "att_" + randomID(controlAttachmentIDByteCount)
	descriptor, err := controlAttachmentDescriptorFromParams(params.Name, params.Kind, params.MIMEType, params.Detail, body)
	if err != nil {
		return attachmentIngestResult{}, err
	}

	dir := s.ControlAttachmentDir(sessionID, id)
	fileDir := filepath.Join(dir, controlAttachmentFileSubdir)
	if err := os.MkdirAll(fileDir, 0o700); err != nil {
		return attachmentIngestResult{}, apperrors.New(http.StatusInternalServerError, "attachment_store_failed", err.Error())
	}
	target := filepath.Join(fileDir, descriptor.Name)
	if err := os.WriteFile(target, body, 0o600); err != nil {
		return attachmentIngestResult{}, apperrors.New(http.StatusInternalServerError, "attachment_store_failed", err.Error())
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
		return attachmentIngestResult{}, apperrors.New(http.StatusInternalServerError, "attachment_store_failed", err.Error())
	}
	return attachmentIngestResult{
		SessionID:  sessionID,
		Attachment: controlAttachmentHandleFromInput(attachment),
	}, nil
}

func (s *Service) StartControlAttachmentIngest(params attachmentIngestStartParams) (attachmentIngestStartResult, error) {
	sessionID := strings.TrimSpace(params.SessionID)
	if sessionID == "" {
		return attachmentIngestStartResult{}, apperrors.New(http.StatusBadRequest, "session_id_required", "session_id required")
	}
	if _, ok := s.store.GetSession(sessionID); !ok {
		return attachmentIngestStartResult{}, apperrors.New(http.StatusNotFound, "session_not_found", "session not found")
	}
	if params.Size < 0 || params.Size > controlAttachmentUploadMaxBytes {
		return attachmentIngestStartResult{}, apperrors.New(http.StatusRequestEntityTooLarge, "attachment_too_large", "attachment is too large")
	}
	expectedSHA := strings.ToLower(strings.TrimSpace(params.SHA256))
	if expectedSHA != "" {
		if _, err := hex.DecodeString(expectedSHA); err != nil || len(expectedSHA) != sha256.Size*2 {
			return attachmentIngestStartResult{}, apperrors.New(http.StatusBadRequest, "attachment_sha256_invalid", "attachment sha256 is invalid")
		}
	}

	id := "att_" + randomID(controlAttachmentIDByteCount)
	descriptor, err := controlAttachmentDescriptorFromParams(params.Name, params.Kind, params.MIMEType, params.Detail, nil)
	if err != nil {
		return attachmentIngestStartResult{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	upload := StoredControlAttachmentUpload{
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
	if err := os.MkdirAll(s.ControlAttachmentDir(sessionID, id), 0o700); err != nil {
		return attachmentIngestStartResult{}, apperrors.New(http.StatusInternalServerError, "attachment_store_failed", err.Error())
	}
	if err := s.WriteControlAttachmentUpload(upload); err != nil {
		return attachmentIngestStartResult{}, apperrors.New(http.StatusInternalServerError, "attachment_store_failed", err.Error())
	}
	return attachmentIngestStartResult{
		SessionID:     sessionID,
		UploadID:      id,
		AttachmentID:  id,
		ChunkMaxBytes: controlAttachmentChunkMaxBytes,
		MaxBytes:      controlAttachmentUploadMaxBytes,
	}, nil
}

func (s *Service) AppendControlAttachmentChunk(params attachmentIngestChunkParams) (attachmentIngestChunkResult, error) {
	upload, err := s.LoadControlAttachmentUpload(params.SessionID, params.UploadID)
	if err != nil {
		return attachmentIngestChunkResult{}, err
	}
	if params.Seq <= 0 || params.Seq != upload.LastSeq+1 {
		return attachmentIngestChunkResult{}, apperrors.New(http.StatusBadRequest, "attachment_chunk_seq_invalid", "attachment chunk seq is invalid")
	}
	if params.Offset != upload.ReceivedBytes {
		return attachmentIngestChunkResult{}, apperrors.New(http.StatusBadRequest, "attachment_chunk_offset_invalid", "attachment chunk offset is invalid")
	}
	encoded := strings.TrimSpace(params.DataBase64)
	if len(encoded) > base64.StdEncoding.EncodedLen(controlAttachmentChunkMaxBytes) {
		return attachmentIngestChunkResult{}, apperrors.New(http.StatusRequestEntityTooLarge, "attachment_chunk_too_large", "attachment chunk is too large")
	}
	body, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return attachmentIngestChunkResult{}, apperrors.New(http.StatusBadRequest, "attachment_chunk_invalid", "attachment chunk data_base64 is invalid")
	}
	if len(body) == 0 {
		return attachmentIngestChunkResult{}, apperrors.New(http.StatusBadRequest, "attachment_chunk_empty", "attachment chunk is empty")
	}
	if len(body) > controlAttachmentChunkMaxBytes {
		return attachmentIngestChunkResult{}, apperrors.New(http.StatusRequestEntityTooLarge, "attachment_chunk_too_large", "attachment chunk is too large")
	}
	nextSize := upload.ReceivedBytes + int64(len(body))
	if nextSize > controlAttachmentUploadMaxBytes || upload.ExpectedSize > 0 && nextSize > upload.ExpectedSize {
		return attachmentIngestChunkResult{}, apperrors.New(http.StatusRequestEntityTooLarge, "attachment_too_large", "attachment is too large")
	}
	partPath := s.ControlAttachmentUploadPartPath(upload.SessionID, upload.UploadID)
	file, err := os.OpenFile(partPath, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return attachmentIngestChunkResult{}, apperrors.New(http.StatusInternalServerError, "attachment_store_failed", err.Error())
	}
	if _, err := file.Seek(upload.ReceivedBytes, io.SeekStart); err != nil {
		file.Close()
		return attachmentIngestChunkResult{}, apperrors.New(http.StatusInternalServerError, "attachment_store_failed", err.Error())
	}
	if _, err := file.Write(body); err != nil {
		file.Close()
		return attachmentIngestChunkResult{}, apperrors.New(http.StatusInternalServerError, "attachment_store_failed", err.Error())
	}
	if err := file.Close(); err != nil {
		return attachmentIngestChunkResult{}, apperrors.New(http.StatusInternalServerError, "attachment_store_failed", err.Error())
	}
	upload.LastSeq = params.Seq
	upload.ReceivedBytes = nextSize
	upload.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := s.WriteControlAttachmentUpload(upload); err != nil {
		return attachmentIngestChunkResult{}, apperrors.New(http.StatusInternalServerError, "attachment_store_failed", err.Error())
	}
	return attachmentIngestChunkResult{
		SessionID:     upload.SessionID,
		UploadID:      upload.UploadID,
		Seq:           upload.LastSeq,
		Offset:        params.Offset,
		ReceivedBytes: upload.ReceivedBytes,
	}, nil
}

func (s *Service) FinishControlAttachmentIngest(params attachmentIngestFinishParams) (attachmentIngestFinishResult, error) {
	upload, err := s.LoadControlAttachmentUpload(params.SessionID, params.UploadID)
	if err != nil {
		return attachmentIngestFinishResult{}, err
	}
	if upload.ExpectedSize > 0 && upload.ReceivedBytes != upload.ExpectedSize {
		return attachmentIngestFinishResult{}, apperrors.New(http.StatusBadRequest, "attachment_size_mismatch", "attachment size does not match expected size")
	}
	partPath := s.ControlAttachmentUploadPartPath(upload.SessionID, upload.UploadID)
	info, err := os.Stat(partPath)
	if os.IsNotExist(err) && upload.ReceivedBytes == 0 {
		if writeErr := os.WriteFile(partPath, nil, 0o600); writeErr != nil {
			return attachmentIngestFinishResult{}, apperrors.New(http.StatusInternalServerError, "attachment_store_failed", writeErr.Error())
		}
		info, err = os.Stat(partPath)
	}
	if err != nil || info.IsDir() {
		return attachmentIngestFinishResult{}, apperrors.New(http.StatusNotFound, "attachment_upload_not_found", "attachment upload not found")
	}
	if info.Size() != upload.ReceivedBytes {
		return attachmentIngestFinishResult{}, apperrors.New(http.StatusBadRequest, "attachment_size_mismatch", "attachment upload size does not match metadata")
	}
	if upload.ExpectedSHA256 != "" {
		actual, err := fileSHA256Hex(partPath)
		if err != nil {
			return attachmentIngestFinishResult{}, apperrors.New(http.StatusInternalServerError, "attachment_hash_failed", err.Error())
		}
		if actual != upload.ExpectedSHA256 {
			return attachmentIngestFinishResult{}, apperrors.New(http.StatusBadRequest, "attachment_sha256_mismatch", "attachment sha256 does not match")
		}
	}
	fileDir := filepath.Join(s.ControlAttachmentDir(upload.SessionID, upload.AttachmentID), controlAttachmentFileSubdir)
	if err := os.MkdirAll(fileDir, 0o700); err != nil {
		return attachmentIngestFinishResult{}, apperrors.New(http.StatusInternalServerError, "attachment_store_failed", err.Error())
	}
	target := filepath.Join(fileDir, upload.Name)
	if err := os.Rename(partPath, target); err != nil {
		return attachmentIngestFinishResult{}, apperrors.New(http.StatusInternalServerError, "attachment_store_failed", err.Error())
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
	if err := writeJSONFile(filepath.Join(s.ControlAttachmentDir(upload.SessionID, upload.AttachmentID), controlAttachmentMetadata), record, 0o600); err != nil {
		return attachmentIngestFinishResult{}, apperrors.New(http.StatusInternalServerError, "attachment_store_failed", err.Error())
	}
	_ = os.Remove(s.ControlAttachmentUploadMetadataPath(upload.SessionID, upload.UploadID))
	return attachmentIngestFinishResult{
		SessionID:  upload.SessionID,
		UploadID:   upload.UploadID,
		Attachment: controlAttachmentHandleFromInput(attachment),
	}, nil
}

func (s *Service) ResolveControlInputAttachments(sessionID string, attachments []InputAttachment) ([]InputAttachment, error) {
	if len(attachments) == 0 {
		return nil, nil
	}
	out := make([]InputAttachment, 0, len(attachments))
	for _, attachment := range attachments {
		attachment.ID = strings.TrimSpace(attachment.ID)
		attachment.Path = strings.TrimSpace(attachment.Path)
		if attachment.Path != "" {
			return nil, apperrors.New(http.StatusBadRequest, "attachment_path_forbidden", "remote attachments must use Host-owned attachment handles")
		}
		if attachment.ID == "" {
			return nil, apperrors.New(http.StatusBadRequest, "attachment_id_required", "attachment id required")
		}
		stored, err := s.LoadControlAttachment(sessionID, attachment.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, stored)
	}
	return out, nil
}

func (s *Service) LoadControlAttachment(sessionID, attachmentID string) (InputAttachment, error) {
	body, err := os.ReadFile(filepath.Join(s.ControlAttachmentDir(sessionID, attachmentID), controlAttachmentMetadata))
	if err != nil {
		if os.IsNotExist(err) {
			return InputAttachment{}, apperrors.New(http.StatusNotFound, "attachment_not_found", "attachment not found")
		}
		return InputAttachment{}, apperrors.New(http.StatusInternalServerError, "attachment_read_failed", err.Error())
	}
	var record storedControlAttachment
	if err := json.Unmarshal(body, &record); err != nil {
		return InputAttachment{}, apperrors.New(http.StatusInternalServerError, "attachment_metadata_invalid", err.Error())
	}
	attachment := record.Attachment
	if record.SessionID != strings.TrimSpace(sessionID) || attachment.ID != strings.TrimSpace(attachmentID) || attachment.Path == "" {
		return InputAttachment{}, apperrors.New(http.StatusNotFound, "attachment_not_found", "attachment not found")
	}
	info, err := os.Stat(attachment.Path)
	if err != nil || info.IsDir() {
		return InputAttachment{}, apperrors.New(http.StatusNotFound, "attachment_file_not_found", "attachment file not found")
	}
	if attachment.Size <= 0 {
		attachment.Size = info.Size()
	}
	return attachment, nil
}

func (s *Service) ControlAttachmentDir(sessionID, attachmentID string) string {
	return filepath.Join(s.store.DataDir(), "runtime", "uploads", safeControlPathSegment(sessionID), safeControlPathSegment(attachmentID))
}

func (s *Service) ControlAttachmentUploadMetadataPath(sessionID, uploadID string) string {
	return filepath.Join(s.ControlAttachmentDir(sessionID, uploadID), controlAttachmentUploadMetadata)
}

func (s *Service) ControlAttachmentUploadPartPath(sessionID, uploadID string) string {
	return filepath.Join(s.ControlAttachmentDir(sessionID, uploadID), controlAttachmentUploadPart)
}

func (s *Service) LoadControlAttachmentUpload(sessionID, uploadID string) (StoredControlAttachmentUpload, error) {
	sessionID = strings.TrimSpace(sessionID)
	uploadID = strings.TrimSpace(uploadID)
	if sessionID == "" || uploadID == "" {
		return StoredControlAttachmentUpload{}, apperrors.New(http.StatusBadRequest, "attachment_upload_reference_invalid", "attachment upload reference is invalid")
	}
	body, err := os.ReadFile(s.ControlAttachmentUploadMetadataPath(sessionID, uploadID))
	if err != nil {
		if os.IsNotExist(err) {
			return StoredControlAttachmentUpload{}, apperrors.New(http.StatusNotFound, "attachment_upload_not_found", "attachment upload not found")
		}
		return StoredControlAttachmentUpload{}, apperrors.New(http.StatusInternalServerError, "attachment_upload_read_failed", err.Error())
	}
	var upload StoredControlAttachmentUpload
	if err := json.Unmarshal(body, &upload); err != nil {
		return StoredControlAttachmentUpload{}, apperrors.New(http.StatusInternalServerError, "attachment_upload_metadata_invalid", err.Error())
	}
	if upload.SessionID != sessionID || upload.UploadID != uploadID || upload.AttachmentID == "" {
		return StoredControlAttachmentUpload{}, apperrors.New(http.StatusNotFound, "attachment_upload_not_found", "attachment upload not found")
	}
	if controlAttachmentUploadExpired(upload, time.Now().UTC()) {
		s.CleanupControlAttachmentUpload(sessionID, uploadID)
		return StoredControlAttachmentUpload{}, apperrors.New(http.StatusGone, "attachment_upload_expired", "attachment upload expired")
	}
	return upload, nil
}

func (s *Service) WriteControlAttachmentUpload(upload StoredControlAttachmentUpload) error {
	return writeJSONFile(s.ControlAttachmentUploadMetadataPath(upload.SessionID, upload.UploadID), upload, 0o600)
}

func (s *Service) CleanupControlAttachmentUpload(sessionID, uploadID string) {
	_ = os.Remove(s.ControlAttachmentUploadMetadataPath(sessionID, uploadID))
	_ = os.Remove(s.ControlAttachmentUploadPartPath(sessionID, uploadID))
	_ = os.Remove(s.ControlAttachmentDir(sessionID, uploadID))
}

func controlAttachmentUploadExpired(upload StoredControlAttachmentUpload, now time.Time) bool {
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
		return controlAttachmentDescriptor{}, apperrors.New(http.StatusRequestEntityTooLarge, "attachment_metadata_too_large", "attachment name is too long")
	}
	explicitMIMEType := strings.TrimSpace(mimeType)
	if len(explicitMIMEType) > controlAttachmentMIMETypeMaxBytes {
		return controlAttachmentDescriptor{}, apperrors.New(http.StatusRequestEntityTooLarge, "attachment_metadata_too_large", "attachment mime_type is too long")
	}
	detail = strings.TrimSpace(detail)
	if len(detail) > controlAttachmentDetailMaxBytes {
		return controlAttachmentDescriptor{}, apperrors.New(http.StatusRequestEntityTooLarge, "attachment_metadata_too_large", "attachment detail is too long")
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

func isNativeImageMIME(mimeType string) bool {
	switch mimeType {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return true
	default:
		return false
	}
}

const (
	ControlAttachmentChunkMaxBytes    = controlAttachmentChunkMaxBytes
	ControlAttachmentNameMaxBytes     = controlAttachmentNameMaxBytes
	ControlAttachmentMIMETypeMaxBytes = controlAttachmentMIMETypeMaxBytes
	ControlAttachmentUploadTTL        = controlAttachmentUploadTTL
)
