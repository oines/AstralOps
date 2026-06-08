package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/oines/astralops/daemon/internal/ports"
	"github.com/oines/astralops/pkg/protocol"
)

type EventsHandler struct {
	Events   ports.EventCommands
	Upgrader websocket.Upgrader
}

func NewEventsHandler(events ports.EventCommands) *EventsHandler {
	return &EventsHandler{
		Events: events,
		Upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		},
	}
}

func (h *EventsHandler) HandleEvents(w http.ResponseWriter, r *http.Request) {
	if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		h.handleEventsWS(w, r)
		return
	}
	if r.URL.Query().Get("stream") == "1" || strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
		h.handleEventsSSE(w, r)
		return
	}
	afterSeq, _ := strconv.ParseInt(r.URL.Query().Get("after_seq"), 10, 64)
	beforeSeq, _ := strconv.ParseInt(r.URL.Query().Get("before_seq"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	events, err := h.Events.QueryEvents(r.Context(), protocol.EventWindowParams{
		WorkspaceID: r.URL.Query().Get("workspace_id"),
		SessionID:   r.URL.Query().Get("session_id"),
		AfterSeq:    afterSeq,
		BeforeSeq:   beforeSeq,
		Limit:       limit,
	})
	if err != nil {
		writeActionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, events)
}

func (h *EventsHandler) handleEventsWS(w http.ResponseWriter, r *http.Request) {
	subscription, err := h.Events.Subscribe(r.Context())
	if err != nil {
		writeActionError(w, err)
		return
	}
	defer subscription.Close()
	conn, err := h.Upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if _, _, err := conn.NextReader(); err != nil {
				return
			}
		}
	}()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-done:
			return
		case event, ok := <-subscription.Events():
			if !ok {
				return
			}
			if err := conn.WriteJSON(event); err != nil {
				return
			}
		}
	}
}

func (h *EventsHandler) handleEventsSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "streaming is not supported"})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	afterSeq, _ := strconv.ParseInt(r.URL.Query().Get("after_seq"), 10, 64)
	params := ports.EventStreamParams{
		WorkspaceID: r.URL.Query().Get("workspace_id"),
		SessionID:   r.URL.Query().Get("session_id"),
		AfterSeq:    afterSeq,
	}

	writeSSE(w, flusher, "heartbeat", map[string]any{"ts": time.Now().UTC().Format(time.RFC3339Nano)})
	events, err := h.Events.ReplayEvents(r.Context(), params)
	if err != nil {
		writeSSE(w, flusher, "error", controlError(err))
		return
	}
	for _, event := range events {
		writeSSE(w, flusher, "astral-event", event)
	}

	subscription, err := h.Events.Subscribe(r.Context())
	if err != nil {
		writeSSE(w, flusher, "error", controlError(err))
		return
	}
	defer subscription.Close()

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			writeSSE(w, flusher, "heartbeat", map[string]any{"ts": time.Now().UTC().Format(time.RFC3339Nano)})
		case event, ok := <-subscription.Events():
			if !ok {
				return
			}
			if afterSeq > 0 && event.Seq <= afterSeq {
				continue
			}
			if params.WorkspaceID != "" && event.WorkspaceID != params.WorkspaceID {
				continue
			}
			if params.SessionID != "" && event.SessionID != params.SessionID {
				continue
			}
			writeSSE(w, flusher, "astral-event", event)
		}
	}
}

func writeSSE(w io.Writer, flusher http.Flusher, name string, payload any) {
	body, _ := json.Marshal(payload)
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, body)
	flusher.Flush()
}
