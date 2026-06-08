package main

import (
	"fmt"
	"strings"

	internalprojection "github.com/oines/astralops/daemon/internal/projection"
	"github.com/oines/astralops/pkg/protocol"
)

type sessionView = protocol.SessionView
type pendingInteractionView = protocol.PendingInteractionView
type interactionDetailRow = protocol.InteractionDetailRow
type queuedInputView = protocol.QueuedInputView
type editableUserMessageView = protocol.EditableUserMessageView

func (a *app) buildSessionView(sessionID string) (sessionView, bool) {
	ss, ok := a.store.getSession(sessionID)
	if !ok {
		return sessionView{}, false
	}
	projectionKey := a.sessionViewProjectionKey(ss)
	return a.sessionProjections().SessionView(sessionID, projectionKey, func() (sessionView, bool) {
		events := a.sessionProjections().QueryEvents("", sessionID, 0)
		pending := projectPendingInteraction(events)
		status := projectedSessionStatus(ss, events, pending != nil)
		title := projectedSessionTitle(firstString(ss.Title, a.store.sessionTitle(sessionID)), events)
		ss.Status = status
		ss.Title = title
		view := sessionView{
			Session:             ss,
			Title:               title,
			Status:              status,
			PendingInteraction:  pending,
			QueuedInputs:        projectQueuedInputs(events),
			EditableUserMessage: projectEditableUserMessage(ss, events, status),
		}
		return view, true
	})
}

func (a *app) sessionViewProjectionKey(ss Session) string {
	parts := []string{
		ss.ID,
		ss.WorkspaceID,
		string(ss.Agent),
		ss.Status,
		ss.Title,
		ss.UpdatedAt,
		fmt.Sprint(a.store.currentSeq()),
	}
	if ss.NativeRef != nil {
		parts = append(parts,
			string(ss.NativeRef.Agent),
			ss.NativeRef.LocalPath,
			ss.NativeRef.NativeSessionID,
			ss.NativeRef.NativeThreadID,
			nativeTranscriptFingerprint(ss.NativeRef.LocalPath),
		)
	}
	if ss.ForkedFromSessionID != "" {
		if source, ok := a.store.getSession(ss.ForkedFromSessionID); ok && source.NativeRef != nil {
			parts = append(parts, ss.ForkedFromSessionID, nativeTranscriptFingerprint(source.NativeRef.LocalPath))
		}
	}
	return strings.Join(parts, "\x00")
}

func projectEditableUserMessage(ss Session, events []AstralEvent, status string) *editableUserMessageView {
	return internalprojection.EditableUserMessage(ss, events, status)
}

func replacedTranscriptSeqs(events []AstralEvent) map[int64]bool {
	return internalprojection.ReplacedTranscriptSeqs(events)
}

func projectedSessionStatus(ss Session, events []AstralEvent, hasPending bool) string {
	return internalprojection.ProjectedSessionStatus(ss, events, hasPending)
}

func projectedSessionTitle(fallback string, events []AstralEvent) string {
	return internalprojection.ProjectedSessionTitle(fallback, events)
}

func projectPendingInteraction(events []AstralEvent) *pendingInteractionView {
	return internalprojection.PendingInteraction(events)
}

func projectQueuedInputs(events []AstralEvent) []queuedInputView {
	return internalprojection.QueuedInputs(events)
}

func interactionIDsFromNormalized(value map[string]any) []string {
	out := []string{}
	for _, key := range []string{"approval_id", "ask_id", "request_id"} {
		if id := stringValue(value[key]); id != "" {
			out = append(out, id)
		}
	}
	return out
}

func firstStringFromSlice(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}
