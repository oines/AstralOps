package main

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	internalworkspace "github.com/oines/astralops/daemon/internal/workspace"
	"github.com/oines/astralops/pkg/protocol"
)

const (
	workspaceFileWriteMaxBytes       = internalworkspace.FileWriteMaxBytes
	workspaceExecOutputMaxBytes      = internalworkspace.ExecOutputMaxBytes
	workspaceFileStreamFrameChunk    = internalworkspace.FileStreamFrameChunk
	workspaceFileStreamFrameComplete = internalworkspace.FileStreamFrameComplete
	workspaceFileStreamFrameError    = internalworkspace.FileStreamFrameError
)

type workspaceFilesReadParams = protocol.WorkspaceFilesReadParams
type workspaceFilesWriteParams = protocol.WorkspaceFilesWriteParams
type workspaceFilesApplyPatchParams = protocol.WorkspaceFilesApplyPatchParams
type workspaceFileTextEdit = protocol.WorkspaceFileTextEdit
type workspaceFilesDeleteParams = protocol.WorkspaceFilesDeleteParams
type workspaceFilesMoveParams = protocol.WorkspaceFilesMoveParams
type workspaceFilesStreamParams = protocol.WorkspaceFilesStreamParams
type workspaceFileStreamCancelParams = protocol.WorkspaceFileStreamCancelParams
type workspaceReferenceParams = protocol.WorkspaceReferenceParams
type workspaceExecParams = protocol.WorkspaceExecParams
type workspaceFileEntry = protocol.WorkspaceFileEntry
type workspaceFilesReadResult = protocol.WorkspaceFilesReadResult
type workspaceFilesWriteResult = protocol.WorkspaceFilesWriteResult
type workspaceFilesApplyPatchResult = protocol.WorkspaceFilesApplyPatchResult
type workspaceFilesDeleteResult = protocol.WorkspaceFilesDeleteResult
type workspaceFilesMoveResult = protocol.WorkspaceFilesMoveResult
type workspaceFileStreamResult = protocol.WorkspaceFileStreamResult
type workspaceFileStreamCancelResult = protocol.WorkspaceFileStreamCancelResult
type workspaceFileStreamFrame = protocol.WorkspaceFileStreamFrame
type workspaceExecResult = protocol.WorkspaceExecResult

func ensureLocalControlWorkspaceExistingPath(root, target string) error {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return workspacePathInvalidError(err)
	}
	realTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		return workspacePathInvalidError(err)
	}
	if !localPathIsSameOrDescendant(filepath.Clean(realRoot), filepath.Clean(realTarget)) {
		return workspacePathInvalidError(errors.New("path escapes workspace through symlink"))
	}
	return nil
}

func workspacePathInvalidError(err error) error {
	return newActionError(http.StatusBadRequest, "workspace_path_invalid", err.Error())
}

func localPathIsSameOrDescendant(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)))
}
