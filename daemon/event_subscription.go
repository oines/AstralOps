package main

import (
	"context"
	"net/http"
	"strings"
)

const (
	eventStreamFrameEvent           = "event"
	eventSubscriptionStreamPrefix   = "event_subscription_"
	eventSubscriptionMaxReplayLimit = 500
)

type eventSubscriptionParams struct {
	WorkspaceID string `json:"workspace_id,omitempty"`
	SessionID   string `json:"session_id,omitempty"`
	AfterSeq    int64  `json:"after_seq,omitempty"`
	ReplayLimit int    `json:"replay_limit,omitempty"`
}

type eventSubscriptionCancelParams struct {
	StreamID string `json:"stream_id"`
}

type eventSubscriptionResult struct {
	StreamID    string `json:"stream_id"`
	WorkspaceID string `json:"workspace_id,omitempty"`
	SessionID   string `json:"session_id,omitempty"`
	AfterSeq    int64  `json:"after_seq,omitempty"`
	ReplayLimit int    `json:"replay_limit,omitempty"`
}

type eventSubscriptionCancelResult struct {
	StreamID  string `json:"stream_id"`
	Cancelled bool   `json:"cancelled"`
}

type eventStreamFrame struct {
	StreamID  string      `json:"stream_id"`
	RequestID string      `json:"request_id,omitempty"`
	Seq       int64       `json:"seq"`
	Event     AstralEvent `json:"event"`
}

func (a *app) prepareControlEventSubscription(params eventSubscriptionParams) (eventSubscriptionResult, error) {
	params.WorkspaceID = strings.TrimSpace(params.WorkspaceID)
	params.SessionID = strings.TrimSpace(params.SessionID)
	if params.AfterSeq < 0 {
		return eventSubscriptionResult{}, newActionError(http.StatusBadRequest, "event_after_seq_invalid", "after_seq must be non-negative")
	}
	if params.ReplayLimit < 0 {
		return eventSubscriptionResult{}, newActionError(http.StatusBadRequest, "event_replay_limit_invalid", "replay_limit must be non-negative")
	}
	if params.ReplayLimit > eventSubscriptionMaxReplayLimit {
		return eventSubscriptionResult{}, newActionError(http.StatusBadRequest, "event_replay_limit_too_large", "replay_limit is too large")
	}
	if params.WorkspaceID != "" {
		if _, ok := a.store.getWorkspace(params.WorkspaceID); !ok {
			return eventSubscriptionResult{}, newActionError(http.StatusNotFound, "workspace_not_found", "workspace not found")
		}
	}
	if params.SessionID != "" {
		session, ok := a.store.getSession(params.SessionID)
		if !ok {
			return eventSubscriptionResult{}, newActionError(http.StatusNotFound, "session_not_found", "session not found")
		}
		if params.WorkspaceID != "" && session.WorkspaceID != params.WorkspaceID {
			return eventSubscriptionResult{}, newActionError(http.StatusNotFound, "session_not_found", "session not found in workspace")
		}
	}
	return eventSubscriptionResult{
		StreamID:    eventSubscriptionStreamPrefix + randomID(16),
		WorkspaceID: params.WorkspaceID,
		SessionID:   params.SessionID,
		AfterSeq:    params.AfterSeq,
		ReplayLimit: params.ReplayLimit,
	}, nil
}

func (a *app) streamControlEvents(ctx context.Context, result eventSubscriptionResult, conn *controlWSConn, requestID string) {
	client := a.hub.addSSE()
	defer a.hub.removeSSE(client)

	latestSeq := result.AfterSeq
	if result.ReplayLimit > 0 {
		for _, event := range a.store.queryEventsWindow(result.WorkspaceID, result.SessionID, result.AfterSeq, 0, result.ReplayLimit) {
			if event.Seq > latestSeq {
				latestSeq = event.Seq
			}
			conn.writePlain(controlPlainFrame{Type: eventStreamFrameEvent, Event: controlEventFrame(result.StreamID, requestID, event)})
		}
	}

	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-client.ch:
			if !ok {
				return
			}
			if event.Seq <= latestSeq || !eventSubscriptionMatches(result, event) {
				continue
			}
			latestSeq = event.Seq
			conn.writePlain(controlPlainFrame{Type: eventStreamFrameEvent, Event: controlEventFrame(result.StreamID, requestID, event)})
		}
	}
}

func controlEventFrame(streamID, requestID string, event AstralEvent) *eventStreamFrame {
	return &eventStreamFrame{
		StreamID:  streamID,
		RequestID: requestID,
		Seq:       event.Seq,
		Event:     event,
	}
}

func eventSubscriptionMatches(result eventSubscriptionResult, event AstralEvent) bool {
	if result.WorkspaceID != "" && event.WorkspaceID != result.WorkspaceID {
		return false
	}
	if result.SessionID != "" && event.SessionID != result.SessionID {
		return false
	}
	return true
}
