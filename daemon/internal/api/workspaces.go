package api

import (
	"net/http"
	"strings"

	"github.com/oines/astralops/daemon/internal/ports"
	"github.com/oines/astralops/pkg/protocol"
)

type WorkspacesHandler struct {
	Workspaces  ports.WorkspaceCommands
	Terminals   ports.TerminalCommands
	Passthrough ports.LegacyWorkspacePassthrough
}

func NewWorkspacesHandler(workspaces ports.WorkspaceCommands, terminals ports.TerminalCommands, passthrough ports.LegacyWorkspacePassthrough) *WorkspacesHandler {
	return &WorkspacesHandler{Workspaces: workspaces, Terminals: terminals, Passthrough: passthrough}
}

func (h *WorkspacesHandler) HandleWorkspaces(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		workspaces, err := h.Workspaces.ReadWorkspaces(r.Context())
		if err != nil {
			writeActionError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, workspaces)
	case http.MethodPost:
		var req protocol.CreateWorkspaceRequest
		if err := decodeJSON(r.Body, &req); err != nil {
			writeDecodeError(w, err)
			return
		}
		workspace, err := h.Workspaces.CreateWorkspace(r.Context(), req)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusCreated, workspace)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (h *WorkspacesHandler) HandleWorkspaceAction(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/v1/workspaces/"), "/")
	if len(parts) == 1 && r.Method == http.MethodDelete {
		if _, err := h.Workspaces.DeleteWorkspace(r.Context(), protocol.WorkspaceReferenceParams{WorkspaceID: parts[0]}); err != nil {
			writeActionError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	if len(parts) < 2 {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	workspaceID, action := parts[0], parts[1]
	switch {
	case action == "native-sessions" && len(parts) == 2 && r.Method == http.MethodGet:
		result, err := h.Workspaces.ListNativeSessions(r.Context(), protocol.NativeSessionsReadParams{WorkspaceID: workspaceID})
		if err != nil {
			writeActionError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	case action == "native-sessions" && len(parts) == 3 && parts[2] == "import" && r.Method == http.MethodPost:
		var req struct {
			SessionID string `json:"session_id"`
		}
		if err := decodeJSON(r.Body, &req); err != nil {
			writeDecodeError(w, err)
			return
		}
		result, err := h.Workspaces.ImportNativeSession(r.Context(), protocol.NativeSessionImportParams{WorkspaceID: workspaceID, SessionID: req.SessionID})
		if err != nil {
			writeActionError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	case action == "files" && len(parts) == 2 && r.Method == http.MethodGet:
		result, err := h.Workspaces.ReadLegacyWorkspaceFiles(r.Context(), ports.LegacyWorkspaceFilesParams{
			WorkspaceID: workspaceID,
			Path:        r.URL.Query().Get("path"),
		})
		if err != nil {
			writeLegacyError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	case action == "terminal" && len(parts) == 2 && r.Method == http.MethodPost:
		open, err := h.Terminals.OpenLegacyWorkspaceTerminal(r.Context(), protocol.WorkspaceReferenceParams{WorkspaceID: workspaceID})
		if err != nil {
			writeActionError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, open)
	case action == "terminals" && len(parts) == 3 && r.Method == http.MethodDelete:
		closed, err := h.Terminals.CloseLegacyWorkspaceTerminal(r.Context(), ports.LegacyWorkspaceTerminalCloseParams{WorkspaceID: workspaceID, TerminalID: parts[2]})
		if err != nil {
			writeLegacyOrActionError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, closed)
	case action == "exec" && len(parts) == 2 && r.Method == http.MethodPost:
		var req struct {
			Command string `json:"command"`
		}
		if err := decodeJSON(r.Body, &req); err != nil {
			writeDecodeError(w, err)
			return
		}
		result, err := h.Workspaces.ExecLegacyWorkspaceCommand(r.Context(), ports.LegacyWorkspaceExecParams{WorkspaceID: workspaceID, Command: req.Command})
		if err != nil {
			writeLegacyError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	case action == "pty" && len(parts) == 2 && strings.EqualFold(r.Header.Get("Upgrade"), "websocket"):
		if h.Passthrough == nil {
			writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "workspace pty is not available"})
			return
		}
		h.Passthrough.ServeWorkspacePTY(w, r, workspaceID)
	case action == "connection" && len(parts) == 2 && r.Method == http.MethodGet:
		connection, err := h.Workspaces.ReadWorkspaceConnection(r.Context(), protocol.WorkspaceReferenceParams{WorkspaceID: workspaceID})
		if err != nil {
			writeLegacyError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, connection)
	case action == "connect" && len(parts) == 2 && r.Method == http.MethodPost:
		connection, err := h.Workspaces.ConnectWorkspace(r.Context(), protocol.WorkspaceReferenceParams{WorkspaceID: workspaceID})
		if err != nil {
			if controlError(err).Code == "workspace_not_found" {
				writeLegacyError(w, err)
				return
			}
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error(), "connection": connection})
			return
		}
		writeJSON(w, http.StatusOK, connection)
	case action == "disconnect" && len(parts) == 2 && r.Method == http.MethodPost:
		connection, err := h.Workspaces.DisconnectWorkspace(r.Context(), protocol.WorkspaceReferenceParams{WorkspaceID: workspaceID})
		if err != nil {
			writeLegacyError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, connection)
	case action == "claude-remote-tool" && len(parts) == 2 && r.Method == http.MethodPost:
		if h.Passthrough == nil {
			writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "claude remote tool is not available"})
			return
		}
		h.Passthrough.ServeClaudeRemoteTool(w, r, workspaceID)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (h *WorkspacesHandler) HandleHostFileSystemBrowse(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var params protocol.HostFileSystemBrowseParams
	if err := decodeJSON(r.Body, &params); err != nil {
		writeDecodeError(w, err)
		return
	}
	result, err := h.Workspaces.BrowseHostFileSystem(r.Context(), params)
	if err != nil {
		writeActionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func writeLegacyError(w http.ResponseWriter, err error) {
	controlErr := controlError(err)
	status := controlErr.Status
	if status == 0 {
		status = http.StatusBadRequest
	}
	writeJSON(w, status, map[string]string{"error": controlErr.Message})
}

func writeLegacyOrActionError(w http.ResponseWriter, err error) {
	switch controlError(err).Code {
	case "workspace_not_found", "terminal_not_found":
		writeLegacyError(w, err)
		return
	}
	if _, ok := err.(controlErrorProvider); ok {
		writeActionError(w, err)
		return
	}
	writeLegacyError(w, err)
}
