package main

import (
	"context"

	internalmedia "github.com/oines/astralops/daemon/internal/media"
	"github.com/oines/astralops/pkg/protocol"
)

const (
	controlAttachmentChunkMaxBytes    = internalmedia.ControlAttachmentChunkMaxBytes
	controlAttachmentNameMaxBytes     = internalmedia.ControlAttachmentNameMaxBytes
	controlAttachmentMIMETypeMaxBytes = internalmedia.ControlAttachmentMIMETypeMaxBytes
	controlAttachmentUploadTTL        = internalmedia.ControlAttachmentUploadTTL
	controlMediaReadMaxBytes          = internalmedia.ControlMediaReadMaxBytes
	mediaStreamFrameChunk             = internalmedia.StreamFrameChunk
	mediaStreamFrameComplete          = internalmedia.StreamFrameComplete
	mediaStreamFrameError             = internalmedia.StreamFrameError
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
type mediaReadParams = protocol.MediaReadParams
type mediaStreamParams = protocol.MediaStreamParams
type mediaStreamCancelParams = protocol.MediaStreamCancelParams
type mediaReadResult = protocol.MediaReadResult
type mediaStreamResult = protocol.MediaStreamResult
type mediaStreamCancelResult = protocol.MediaStreamCancelResult
type mediaStreamFrame = protocol.MediaStreamFrame
type resolvedSessionMedia = internalmedia.ResolvedSessionMedia
type storedControlAttachmentUpload = internalmedia.StoredControlAttachmentUpload

type mediaService struct {
	service *internalmedia.Service
}

func (a *app) mediaService() *mediaService {
	return &mediaService{service: internalmedia.New(mediaStoreAdapter{store: a.store, queryEvents: a.sessionProjections().QueryEvents})}
}

type mediaStoreAdapter struct {
	store       *store
	queryEvents func(workspaceID, sessionID string, afterSeq int64) []AstralEvent
}

func (s mediaStoreAdapter) DataDir() string {
	if s.store == nil {
		return ""
	}
	return s.store.dataDir
}

func (s mediaStoreAdapter) GetSession(id string) (Session, bool) {
	if s.store == nil {
		return Session{}, false
	}
	return s.store.getSession(id)
}

func (s mediaStoreAdapter) QueryEvents(workspaceID, sessionID string, afterSeq int64) []AstralEvent {
	if s.queryEvents != nil {
		return s.queryEvents(workspaceID, sessionID, afterSeq)
	}
	return nil
}

type mediaControlStream struct {
	conn controlConnection
}

func (s mediaControlStream) WriteMedia(frameType string, frame *mediaStreamFrame) {
	if s.conn == nil {
		return
	}
	s.conn.writePlain(controlPlainFrame{Type: frameType, Media: frame})
}

func sanitizeInputAttachments(attachments []InputAttachment) []InputAttachment {
	return internalmedia.SanitizeInputAttachments(attachments)
}

func attachmentsFromNormalized(value any) []InputAttachment {
	return internalmedia.AttachmentsFromNormalized(value)
}

func mediaStreamChunkSize(requested int) int {
	return internalmedia.MediaStreamChunkSize(requested)
}

func mediaStreamResumeToken(sessionID string, eventSeq int64, mediaID string) string {
	return internalmedia.MediaStreamResumeToken(sessionID, eventSeq, mediaID)
}

func (s *mediaService) ingestControlAttachment(params attachmentIngestParams) (attachmentIngestResult, error) {
	return s.service.IngestControlAttachment(params)
}

func (s *mediaService) startControlAttachmentIngest(params attachmentIngestStartParams) (attachmentIngestStartResult, error) {
	return s.service.StartControlAttachmentIngest(params)
}

func (s *mediaService) appendControlAttachmentChunk(params attachmentIngestChunkParams) (attachmentIngestChunkResult, error) {
	return s.service.AppendControlAttachmentChunk(params)
}

func (s *mediaService) finishControlAttachmentIngest(params attachmentIngestFinishParams) (attachmentIngestFinishResult, error) {
	return s.service.FinishControlAttachmentIngest(params)
}

func (s *mediaService) resolveControlInputAttachments(sessionID string, attachments []InputAttachment) ([]InputAttachment, error) {
	return s.service.ResolveControlInputAttachments(sessionID, attachments)
}

func (s *mediaService) loadControlAttachment(sessionID, attachmentID string) (InputAttachment, error) {
	return s.service.LoadControlAttachment(sessionID, attachmentID)
}

func (s *mediaService) controlAttachmentDir(sessionID, attachmentID string) string {
	return s.service.ControlAttachmentDir(sessionID, attachmentID)
}

func (s *mediaService) controlAttachmentUploadMetadataPath(sessionID, uploadID string) string {
	return s.service.ControlAttachmentUploadMetadataPath(sessionID, uploadID)
}

func (s *mediaService) controlAttachmentUploadPartPath(sessionID, uploadID string) string {
	return s.service.ControlAttachmentUploadPartPath(sessionID, uploadID)
}

func (s *mediaService) loadControlAttachmentUpload(sessionID, uploadID string) (storedControlAttachmentUpload, error) {
	return s.service.LoadControlAttachmentUpload(sessionID, uploadID)
}

func (s *mediaService) writeControlAttachmentUpload(upload storedControlAttachmentUpload) error {
	return s.service.WriteControlAttachmentUpload(upload)
}

func (s *mediaService) cleanupControlAttachmentUpload(sessionID, uploadID string) {
	s.service.CleanupControlAttachmentUpload(sessionID, uploadID)
}

func (s *mediaService) prepareControlMediaStream(params mediaStreamParams) (mediaStreamResult, error) {
	return s.service.PrepareControlMediaStream(params)
}

func (s *mediaService) streamControlMedia(ctx context.Context, result mediaStreamResult, conn controlConnection, requestID string) {
	s.service.StreamControlMedia(ctx, result, mediaControlStream{conn: conn}, requestID)
}

func (s *mediaService) readControlMedia(params mediaReadParams, download bool) (mediaReadResult, error) {
	return s.service.ReadControlMedia(params, download)
}

func (s *mediaService) resolveSessionMedia(sessionID string, eventSeq int64, mediaID string) (resolvedSessionMedia, error) {
	return s.service.ResolveSessionMedia(sessionID, eventSeq, mediaID)
}

func (a *app) ingestControlAttachment(params attachmentIngestParams) (attachmentIngestResult, error) {
	return a.mediaService().ingestControlAttachment(params)
}

func (a *app) startControlAttachmentIngest(params attachmentIngestStartParams) (attachmentIngestStartResult, error) {
	return a.mediaService().startControlAttachmentIngest(params)
}

func (a *app) appendControlAttachmentChunk(params attachmentIngestChunkParams) (attachmentIngestChunkResult, error) {
	return a.mediaService().appendControlAttachmentChunk(params)
}

func (a *app) finishControlAttachmentIngest(params attachmentIngestFinishParams) (attachmentIngestFinishResult, error) {
	return a.mediaService().finishControlAttachmentIngest(params)
}

func (a *app) resolveControlInputAttachments(sessionID string, attachments []InputAttachment) ([]InputAttachment, error) {
	return a.mediaService().resolveControlInputAttachments(sessionID, attachments)
}

func (a *app) loadControlAttachment(sessionID, attachmentID string) (InputAttachment, error) {
	return a.mediaService().loadControlAttachment(sessionID, attachmentID)
}

func (a *app) controlAttachmentDir(sessionID, attachmentID string) string {
	return a.mediaService().controlAttachmentDir(sessionID, attachmentID)
}

func (a *app) controlAttachmentUploadMetadataPath(sessionID, uploadID string) string {
	return a.mediaService().controlAttachmentUploadMetadataPath(sessionID, uploadID)
}

func (a *app) controlAttachmentUploadPartPath(sessionID, uploadID string) string {
	return a.mediaService().controlAttachmentUploadPartPath(sessionID, uploadID)
}

func (a *app) loadControlAttachmentUpload(sessionID, uploadID string) (storedControlAttachmentUpload, error) {
	return a.mediaService().loadControlAttachmentUpload(sessionID, uploadID)
}

func (a *app) writeControlAttachmentUpload(upload storedControlAttachmentUpload) error {
	return a.mediaService().writeControlAttachmentUpload(upload)
}

func (a *app) cleanupControlAttachmentUpload(sessionID, uploadID string) {
	a.mediaService().cleanupControlAttachmentUpload(sessionID, uploadID)
}

func (a *app) prepareControlMediaStream(params mediaStreamParams) (mediaStreamResult, error) {
	return a.mediaService().prepareControlMediaStream(params)
}

func (a *app) streamControlMedia(ctx context.Context, result mediaStreamResult, conn controlConnection, requestID string) {
	a.mediaService().streamControlMedia(ctx, result, conn, requestID)
}

func (a *app) readControlMedia(params mediaReadParams, download bool) (mediaReadResult, error) {
	return a.mediaService().readControlMedia(params, download)
}

func (a *app) resolveSessionMedia(sessionID string, eventSeq int64, mediaID string) (resolvedSessionMedia, error) {
	return a.mediaService().resolveSessionMedia(sessionID, eventSeq, mediaID)
}
