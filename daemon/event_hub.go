package main

import (
	"log"
	"sync"

	"github.com/gorilla/websocket"
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
	saved, err := a.store.appendEvent(ev)
	if err != nil {
		log.Printf("append event: %v", err)
		return
	}
	a.hub.broadcast(saved)
	notificationTitle, targetSessionID := a.notificationTarget(saved)
	if notification, ok := notificationEventForSource(saved, notificationTitle, targetSessionID, a.store.allEvents()); ok {
		savedNotification, err := a.store.appendEvent(notification)
		if err != nil {
			log.Printf("append notification event: %v", err)
			return
		}
		a.hub.broadcast(savedNotification)
	}
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
