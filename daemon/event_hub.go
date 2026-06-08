package main

import (
	"context"
	"log"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/oines/astralops/daemon/internal/eventlog"
)

type eventHub struct {
	mu         sync.Mutex
	wsClients  map[*websocket.Conn]bool
	sseClients map[*sseClient]bool
}

type sseClient struct {
	ch chan AstralEvent
}

func (a *app) emit(ev AstralEvent) {
	_, _ = a.eventPublisher().Publish(context.Background(), ev)
}

func (a *app) eventPublisher() *eventlog.Service {
	if a.eventLog == nil {
		a.eventLog = eventlog.New(eventlog.Options{
			Store:         a.store,
			Projections:   a.sessionProjections(),
			Broadcaster:   a.hub,
			Notifications: appNotificationPolicy{target: a.notificationTarget},
			History:       a.notificationHistory,
			Diagnostics:   logDiagnosticEvent,
		})
	}
	return a.eventLog
}

func (a *app) notificationHistory(source AstralEvent, targetSessionID string) []AstralEvent {
	if a == nil || a.store == nil {
		return []AstralEvent{source}
	}
	sessionID := firstString(targetSessionID, source.SessionID)
	return a.sessionProjections().QueryEvents(source.WorkspaceID, sessionID, 0)
}

type appNotificationPolicy struct {
	target func(AstralEvent) (string, string)
}

func (p appNotificationPolicy) Target(ev AstralEvent) (string, string) {
	if p.target == nil {
		return "", ""
	}
	return p.target(ev)
}

func (p appNotificationPolicy) Build(source AstralEvent, title string, targetSessionID string, events []AstralEvent) (AstralEvent, bool) {
	return notificationEventForSource(source, title, targetSessionID, events)
}

func (a *app) notificationTarget(ev AstralEvent) (string, string) {
	if ev.SessionID != "" {
		return a.store.sessionTitle(ev.SessionID), ev.SessionID
	}
	if ev.WorkspaceID == "" {
		return "", ""
	}
	if sessionID, ok := a.store.latestSessionIDForWorkspace(ev.WorkspaceID); ok {
		return a.store.sessionTitle(sessionID), sessionID
	}
	if ws, ok := a.store.getWorkspace(ev.WorkspaceID); ok {
		return ws.Name, ""
	}
	return "", ""
}

func newEventHub() *eventHub {
	return &eventHub{
		wsClients:  map[*websocket.Conn]bool{},
		sseClients: map[*sseClient]bool{},
	}
}

func (h *eventHub) add(c *websocket.Conn) {
	h.mu.Lock()
	h.wsClients[c] = true
	h.mu.Unlock()
}

func (h *eventHub) remove(c *websocket.Conn) {
	h.mu.Lock()
	delete(h.wsClients, c)
	h.mu.Unlock()
	_ = c.Close()
}

func (h *eventHub) addSSE() *sseClient {
	client := &sseClient{ch: make(chan AstralEvent, 256)}
	h.mu.Lock()
	h.sseClients[client] = true
	h.mu.Unlock()
	return client
}

func (h *eventHub) removeSSE(client *sseClient) {
	h.mu.Lock()
	if h.sseClients[client] {
		delete(h.sseClients, client)
		close(client.ch)
	}
	h.mu.Unlock()
}

func (h *eventHub) broadcast(ev AstralEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.wsClients {
		if err := c.WriteJSON(ev); err != nil {
			_ = c.Close()
			delete(h.wsClients, c)
		}
	}
	for client := range h.sseClients {
		select {
		case client.ch <- ev:
		default:
			delete(h.sseClients, client)
			close(client.ch)
		}
	}
}

func (h *eventHub) Broadcast(ev AstralEvent) {
	if h == nil {
		log.Printf("event broadcaster is not initialized")
		return
	}
	h.broadcast(ev)
}
