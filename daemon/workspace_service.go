package main

import (
	"context"
	"time"

	internalworkspace "github.com/oines/astralops/daemon/internal/workspace"
)

type workspaceService struct {
	service *internalworkspace.Service
}

func (a *app) workspaceService() *workspaceService {
	var ssh internalworkspace.SSHService
	if a.ssh != nil {
		ssh = workspaceSSHAdapter{ssh: a.ssh}
	}
	return &workspaceService{service: internalworkspace.New(workspaceStoreAdapter{store: a.store}, ssh, a.runRemoteWorkspaceExecAt)}
}

type workspaceStoreAdapter struct {
	store *store
}

func (s workspaceStoreAdapter) GetWorkspace(id string) (Workspace, bool) {
	if s.store == nil {
		return Workspace{}, false
	}
	return s.store.getWorkspace(id)
}

type workspaceSSHAdapter struct {
	ssh *sshManager
}

func (s workspaceSSHAdapter) Call(ctx context.Context, ws Workspace, action string, params any, out any) error {
	if s.ssh == nil {
		return newActionError(501, "ssh_unavailable", "ssh manager unavailable")
	}
	return s.ssh.call(ctx, ws, action, params, out)
}

type workspaceFileControlStream struct {
	conn controlConnection
}

func (s workspaceFileControlStream) WriteWorkspaceFile(frameType string, frame *workspaceFileStreamFrame) {
	if s.conn == nil {
		return
	}
	s.conn.writePlain(controlPlainFrame{Type: frameType, WorkspaceFile: frame})
}

func (s *workspaceService) controlWorkspace(workspaceID string) (Workspace, error) {
	return s.service.ControlWorkspace(workspaceID)
}

func (s *workspaceService) readControlWorkspaceFiles(ctx context.Context, params workspaceFilesReadParams) (workspaceFilesReadResult, error) {
	return s.service.ReadControlWorkspaceFiles(ctx, params)
}

func (s *workspaceService) writeControlWorkspaceFile(ctx context.Context, params workspaceFilesWriteParams) (workspaceFilesWriteResult, error) {
	return s.service.WriteControlWorkspaceFile(ctx, params)
}

func (s *workspaceService) applyControlWorkspacePatch(ctx context.Context, params workspaceFilesApplyPatchParams) (workspaceFilesApplyPatchResult, error) {
	return s.service.ApplyControlWorkspacePatch(ctx, params)
}

func (s *workspaceService) deleteControlWorkspacePath(ctx context.Context, params workspaceFilesDeleteParams) (workspaceFilesDeleteResult, error) {
	return s.service.DeleteControlWorkspacePath(ctx, params)
}

func (s *workspaceService) moveControlWorkspacePath(ctx context.Context, params workspaceFilesMoveParams) (workspaceFilesMoveResult, error) {
	return s.service.MoveControlWorkspacePath(ctx, params)
}

func (s *workspaceService) prepareControlWorkspaceFileStream(ctx context.Context, params workspaceFilesStreamParams) (workspaceFileStreamResult, error) {
	return s.service.PrepareControlWorkspaceFileStream(ctx, params)
}

func (s *workspaceService) streamControlWorkspaceFile(ctx context.Context, params workspaceFilesStreamParams, result workspaceFileStreamResult, conn controlConnection, requestID string) {
	s.service.StreamControlWorkspaceFile(ctx, params, result, workspaceFileControlStream{conn: conn}, requestID)
}

func (s *workspaceService) executeControlWorkspaceCommand(ctx context.Context, params workspaceExecParams, grant TrustGrant) (workspaceExecResult, error) {
	return s.service.ExecuteControlWorkspaceCommand(ctx, params, grant.WorkspaceExecPolicy)
}

func (s *workspaceService) executeRemoteControlWorkspaceCommand(parent context.Context, ws Workspace, command, requestedCWD string, timeout time.Duration, approvalPolicy string) (workspaceExecResult, error) {
	return s.service.ExecuteRemoteControlWorkspaceCommand(parent, ws, command, requestedCWD, timeout, approvalPolicy)
}

func (s *workspaceService) remoteWorkspaceEntries(ctx context.Context, ws Workspace, root, target string) ([]workspaceFileEntry, bool, error) {
	return s.service.RemoteWorkspaceEntries(ctx, ws, root, target)
}

func (a *app) controlWorkspace(workspaceID string) (Workspace, error) {
	return a.workspaceService().controlWorkspace(workspaceID)
}

func (a *app) readControlWorkspaceFiles(ctx context.Context, params workspaceFilesReadParams) (workspaceFilesReadResult, error) {
	return a.workspaceService().readControlWorkspaceFiles(ctx, params)
}

func (a *app) writeControlWorkspaceFile(ctx context.Context, params workspaceFilesWriteParams) (workspaceFilesWriteResult, error) {
	return a.workspaceService().writeControlWorkspaceFile(ctx, params)
}

func (a *app) applyControlWorkspacePatch(ctx context.Context, params workspaceFilesApplyPatchParams) (workspaceFilesApplyPatchResult, error) {
	return a.workspaceService().applyControlWorkspacePatch(ctx, params)
}

func (a *app) deleteControlWorkspacePath(ctx context.Context, params workspaceFilesDeleteParams) (workspaceFilesDeleteResult, error) {
	return a.workspaceService().deleteControlWorkspacePath(ctx, params)
}

func (a *app) moveControlWorkspacePath(ctx context.Context, params workspaceFilesMoveParams) (workspaceFilesMoveResult, error) {
	return a.workspaceService().moveControlWorkspacePath(ctx, params)
}

func (a *app) prepareControlWorkspaceFileStream(ctx context.Context, params workspaceFilesStreamParams) (workspaceFileStreamResult, error) {
	return a.workspaceService().prepareControlWorkspaceFileStream(ctx, params)
}

func (a *app) streamControlWorkspaceFile(ctx context.Context, params workspaceFilesStreamParams, result workspaceFileStreamResult, conn controlConnection, requestID string) {
	a.workspaceService().streamControlWorkspaceFile(ctx, params, result, conn, requestID)
}

func (a *app) executeControlWorkspaceCommand(ctx context.Context, params workspaceExecParams, grant TrustGrant) (workspaceExecResult, error) {
	return a.workspaceService().executeControlWorkspaceCommand(ctx, params, grant)
}

func (a *app) executeRemoteControlWorkspaceCommand(parent context.Context, ws Workspace, command, requestedCWD string, timeout time.Duration, approvalPolicy string) (workspaceExecResult, error) {
	return a.workspaceService().executeRemoteControlWorkspaceCommand(parent, ws, command, requestedCWD, timeout, approvalPolicy)
}

func (a *app) remoteWorkspaceEntries(ctx context.Context, ws Workspace, root, target string) ([]workspaceFileEntry, bool, error) {
	return a.workspaceService().remoteWorkspaceEntries(ctx, ws, root, target)
}
