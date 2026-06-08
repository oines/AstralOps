package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/oines/astralops/daemon/internal/ports"
	"github.com/oines/astralops/pkg/protocol"
)

type SessionsHandler struct {
	Sessions ports.SessionCommands
	Media    ports.MediaCommands
}

func NewSessionsHandler(sessions ports.SessionCommands, media ports.MediaCommands) *SessionsHandler {
	return &SessionsHandler{Sessions: sessions, Media: media}
}

func (h *SessionsHandler) HandleSessions(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		sessions, err := h.Sessions.ReadSessions(r.Context(), protocol.SessionsReadParams{WorkspaceID: r.URL.Query().Get("workspace_id")})
		if err != nil {
			writeActionError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, sessions)
	case http.MethodPost:
		var req protocol.CreateSessionRequest
		if err := decodeJSON(r.Body, &req); err != nil {
			writeDecodeError(w, err)
			return
		}
		session, err := h.Sessions.CreateSession(r.Context(), req)
		if err != nil {
			writeActionError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, session)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (h *SessionsHandler) HandleSessionAction(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/v1/sessions/"), "/")
	if len(parts) == 1 && r.Method == http.MethodDelete {
		if _, err := h.Sessions.DeleteSession(r.Context(), protocol.SessionDeleteParams{SessionID: parts[0]}); err != nil {
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

	sessionID, action := parts[0], parts[1]
	switch {
	case action == "view" && r.Method == http.MethodGet:
		view, err := h.Sessions.ReadSessionView(r.Context(), protocol.SessionReferenceParams{SessionID: sessionID})
		if err != nil {
			writeActionError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, view)
	case action == "fork" && r.Method == http.MethodPost:
		h.handleForkSession(w, r, sessionID)
	case action == "commands" && len(parts) == 2 && r.Method == http.MethodGet:
		commands, err := h.Sessions.ListCommands(r.Context(), protocol.SessionReferenceParams{SessionID: sessionID})
		if err != nil {
			writeActionError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, commands)
	case action == "commands" && len(parts) == 3 && r.Method == http.MethodPost:
		h.handleRunSessionCommand(w, r, sessionID, parts[2])
	case action == "input" && r.Method == http.MethodPost:
		h.handleSessionInput(w, r, sessionID)
	case action == "media" && len(parts) == 4 && r.Method == http.MethodGet:
		h.handleSessionMedia(w, r, sessionID, parts[2], parts[3])
	case action == "edit-last-user-message" && r.Method == http.MethodPost:
		h.handleEditLastUserMessage(w, r, sessionID)
	case action == "interrupt" && r.Method == http.MethodPost:
		result, err := h.Sessions.CancelTurn(r.Context(), protocol.SessionReferenceParams{SessionID: sessionID})
		if err != nil {
			writeActionError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	case action == "queue" && len(parts) == 4 && parts[3] == "cancel" && r.Method == http.MethodPost:
		if _, err := h.Sessions.CancelQueue(r.Context(), protocol.QueueControlParams{SessionID: sessionID, QueueID: parts[2]}); err != nil {
			writeActionError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	case action == "queue" && len(parts) == 4 && parts[3] == "steer" && r.Method == http.MethodPost:
		if _, err := h.Sessions.SteerQueue(r.Context(), protocol.QueueControlParams{SessionID: sessionID, QueueID: parts[2]}); err != nil {
			writeActionError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (h *SessionsHandler) handleSessionInput(w http.ResponseWriter, r *http.Request, sessionID string) {
	var req struct {
		Input           string                     `json:"input"`
		Model           string                     `json:"model"`
		ReasoningEffort string                     `json:"reasoning_effort"`
		PermissionMode  string                     `json:"permission_mode"`
		Attachments     []protocol.InputAttachment `json:"attachments"`
	}
	if err := decodeJSON(r.Body, &req); err != nil {
		writeDecodeError(w, err)
		return
	}
	result, err := h.Sessions.StartInput(r.Context(), ports.SessionInputParams{
		SessionID:       sessionID,
		Input:           req.Input,
		Model:           req.Model,
		ReasoningEffort: req.ReasoningEffort,
		PermissionMode:  req.PermissionMode,
		Attachments:     req.Attachments,
	})
	if err != nil {
		writeActionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *SessionsHandler) handleForkSession(w http.ResponseWriter, r *http.Request, sessionID string) {
	var req protocol.ForkSessionRequest
	if err := decodeJSON(r.Body, &req); err != nil {
		writeDecodeError(w, err)
		return
	}
	response, err := h.Sessions.ForkSession(r.Context(), protocol.SessionForkControlParams{SessionID: sessionID, EventSeq: req.EventSeq})
	if err != nil {
		writeActionError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, response)
}

func (h *SessionsHandler) handleEditLastUserMessage(w http.ResponseWriter, r *http.Request, sessionID string) {
	var req protocol.EditLastUserMessageRequest
	if err := decodeJSON(r.Body, &req); err != nil {
		writeDecodeError(w, err)
		return
	}
	result, err := h.Sessions.EditLastUserMessage(r.Context(), protocol.SessionEditParams{
		SessionID:       sessionID,
		EventSeq:        req.EventSeq,
		Input:           req.Input,
		Model:           req.Model,
		ReasoningEffort: req.ReasoningEffort,
		PermissionMode:  req.PermissionMode,
	})
	if err != nil {
		writeActionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *SessionsHandler) handleRunSessionCommand(w http.ResponseWriter, r *http.Request, sessionID string, commandID string) {
	var req protocol.SessionCommandRequest
	if err := decodeJSON(r.Body, &req); err != nil {
		writeDecodeError(w, err)
		return
	}
	result, err := h.Sessions.RunCommand(r.Context(), ports.SessionCommandRunParams{
		SessionID: sessionID,
		CommandID: commandID,
		Request:   req,
	})
	if err != nil {
		writeActionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *SessionsHandler) handleSessionMedia(w http.ResponseWriter, r *http.Request, sessionID, seqText, mediaID string) {
	if h.Media == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "media service is not initialized"})
		return
	}
	seq, err := strconv.ParseInt(seqText, 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid media reference"})
		return
	}
	media, err := h.Media.ResolveSessionMedia(r.Context(), ports.SessionMediaParams{SessionID: sessionID, EventSeq: seq, MediaID: mediaID})
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
