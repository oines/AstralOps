package sessions

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/oines/astralops/daemon/internal/agents"
	"github.com/oines/astralops/daemon/internal/sessiontypes"
	"github.com/oines/astralops/pkg/protocol"
)

type Store interface {
	GetSession(id string) (protocol.Session, bool)
	GetWorkspace(id string) (protocol.Workspace, bool)
	CreateSession(workspace protocol.Workspace, agent protocol.AgentKind) protocol.Session
	CreateForkSession(workspace protocol.Workspace, source protocol.Session, anchor sessiontypes.ForkAnchor) protocol.Session
	DeleteSession(id string)
	UpdateSessionStatus(id, status string)
	QueryEvents(workspaceID, sessionID string, afterSeq int64) []protocol.AstralEvent
	SessionTitle(sessionID string) string
}

type QueueService struct {
	store   Store
	agents  map[protocol.AgentKind]agents.Runtime
	queueMu sync.Mutex
	queues  map[string][]sessiontypes.QueuedTurn
	emit    func(protocol.AstralEvent)
}

func NewQueueService(store Store, agentsRegistry map[protocol.AgentKind]agents.Runtime, emit func(protocol.AstralEvent)) *QueueService {
	return &QueueService{store: store, agents: agentsRegistry, queues: map[string][]sessiontypes.QueuedTurn{}, emit: emit}
}

func (s *QueueService) UpdateDependencies(store Store, agentsRegistry map[protocol.AgentKind]agents.Runtime, emit func(protocol.AstralEvent)) {
	if s == nil {
		return
	}
	s.store = store
	s.agents = agentsRegistry
	s.emit = emit
}

func (s *QueueService) EnsureQueues() map[string][]sessiontypes.QueuedTurn {
	if s.queues == nil {
		s.queues = map[string][]sessiontypes.QueuedTurn{}
	}
	return s.queues
}

func (s *QueueService) EnqueueTurn(session protocol.Session, input string, options sessiontypes.TurnOptions) sessiontypes.QueuedTurn {
	turn := sessiontypes.QueuedTurn{
		ID:        "queue_" + randomID(12),
		Input:     input,
		Options:   options,
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	s.queueMu.Lock()
	queues := s.EnsureQueues()
	queues[session.ID] = append(queues[session.ID], turn)
	position := len(queues[session.ID])
	s.queueMu.Unlock()

	normalized := map[string]any{
		"queue_id": turn.ID,
		"position": position,
	}
	if options.Internal {
		normalized["internal"] = true
	}
	if text := turnDisplayInput(input, options); text != "" {
		normalized["text"] = text
	}
	s.emit(protocol.AstralEvent{WorkspaceID: session.WorkspaceID, SessionID: session.ID, Agent: session.Agent, Kind: "queue.queued", Normalized: protocol.EventNormalized("queue.queued", normalized)})
	return turn
}

func (s *QueueService) CancelQueuedTurn(sessionID, queueID string) {
	session, _ := s.store.GetSession(sessionID)
	cancelled := false
	s.queueMu.Lock()
	queues := s.EnsureQueues()
	queue := queues[sessionID]
	next := queue[:0]
	for _, turn := range queue {
		if turn.ID == queueID {
			cancelled = true
			continue
		}
		next = append(next, turn)
	}
	if len(next) == 0 {
		delete(queues, sessionID)
	} else {
		queues[sessionID] = next
	}
	s.queueMu.Unlock()
	if cancelled {
		s.emit(protocol.AstralEvent{WorkspaceID: session.WorkspaceID, SessionID: sessionID, Agent: session.Agent, Kind: "queue.cancelled", Normalized: protocol.EventNormalized("queue.cancelled", map[string]any{"queue_id": queueID})})
	}
}

func (s *QueueService) ClearSessionQueue(sessionID string, reason string) {
	session, _ := s.store.GetSession(sessionID)
	s.queueMu.Lock()
	queues := s.EnsureQueues()
	queue := append([]sessiontypes.QueuedTurn(nil), queues[sessionID]...)
	delete(queues, sessionID)
	s.queueMu.Unlock()
	for _, turn := range queue {
		normalized := map[string]any{"queue_id": turn.ID}
		if reason != "" {
			normalized["reason"] = reason
		}
		if turn.Options.Internal {
			normalized["internal"] = true
		}
		if text := turnDisplayInput(turn.Input, turn.Options); text != "" {
			normalized["text"] = text
		}
		s.emit(protocol.AstralEvent{WorkspaceID: session.WorkspaceID, SessionID: sessionID, Agent: session.Agent, Kind: "queue.cancelled", Normalized: protocol.EventNormalized("queue.cancelled", normalized)})
	}
}

func (s *QueueService) SteerQueuedTurn(sessionID, queueID string) error {
	ss, ok := s.store.GetSession(sessionID)
	if !ok {
		return fmt.Errorf("session not found")
	}
	runtime, ok := s.agents[ss.Agent]
	if !ok {
		return fmt.Errorf("agent runtime is not implemented")
	}
	steerer, ok := runtime.(agents.Steerer)
	if !ok {
		return sessiontypes.ErrSteerUnsupported
	}
	turn, ok := s.PeekQueuedTurn(sessionID, queueID)
	if !ok {
		return fmt.Errorf("queued message not found")
	}
	if err := steerer.Steer(sessionID, turn.Input, turn.Options); err != nil {
		return err
	}
	if !s.RemoveQueuedTurn(sessionID, queueID) {
		return fmt.Errorf("queued message not found")
	}
	normalized := map[string]any{"queue_id": queueID}
	if turn.Options.Internal {
		normalized["internal"] = true
	}
	if text := turnDisplayInput(turn.Input, turn.Options); text != "" {
		normalized["text"] = text
	}
	s.emit(protocol.AstralEvent{WorkspaceID: ss.WorkspaceID, SessionID: ss.ID, Agent: ss.Agent, Kind: "queue.steered", Normalized: protocol.EventNormalized("queue.steered", normalized)})
	return nil
}

func (s *QueueService) PopQueuedTurn(sessionID string) (sessiontypes.QueuedTurn, bool) {
	s.queueMu.Lock()
	defer s.queueMu.Unlock()
	queues := s.EnsureQueues()
	queue := queues[sessionID]
	if len(queue) == 0 {
		return sessiontypes.QueuedTurn{}, false
	}
	turn := queue[0]
	if len(queue) == 1 {
		delete(queues, sessionID)
	} else {
		queues[sessionID] = append([]sessiontypes.QueuedTurn(nil), queue[1:]...)
	}
	return turn, true
}

func (s *QueueService) PeekQueuedTurn(sessionID, queueID string) (sessiontypes.QueuedTurn, bool) {
	s.queueMu.Lock()
	defer s.queueMu.Unlock()
	queues := s.EnsureQueues()
	for _, turn := range queues[sessionID] {
		if turn.ID == queueID {
			return turn, true
		}
	}
	return sessiontypes.QueuedTurn{}, false
}

func (s *QueueService) RemoveQueuedTurn(sessionID, queueID string) bool {
	s.queueMu.Lock()
	defer s.queueMu.Unlock()
	queues := s.EnsureQueues()
	queue := queues[sessionID]
	next := queue[:0]
	removed := false
	for _, turn := range queue {
		if turn.ID == queueID {
			removed = true
			continue
		}
		next = append(next, turn)
	}
	if len(next) == 0 {
		delete(queues, sessionID)
	} else {
		queues[sessionID] = next
	}
	return removed
}

func (s *QueueService) RequeueFront(sessionID string, turn sessiontypes.QueuedTurn) {
	s.queueMu.Lock()
	defer s.queueMu.Unlock()
	queues := s.EnsureQueues()
	queues[sessionID] = append([]sessiontypes.QueuedTurn{turn}, queues[sessionID]...)
}

func (s *QueueService) Snapshot(sessionID string) []sessiontypes.QueuedTurn {
	s.queueMu.Lock()
	defer s.queueMu.Unlock()
	queues := s.EnsureQueues()
	return append([]sessiontypes.QueuedTurn(nil), queues[sessionID]...)
}

func turnDisplayInput(input string, options sessiontypes.TurnOptions) string {
	if text := strings.TrimSpace(options.DisplayInput); text != "" {
		return text
	}
	if options.Internal {
		return ""
	}
	if text := strings.TrimSpace(input); text != "" {
		return text
	}
	if len(options.Attachments) == 1 {
		name := options.Attachments[0].Name
		if name == "" {
			name = options.Attachments[0].Path
		}
		return "附件：" + name
	}
	if len(options.Attachments) > 1 {
		return fmt.Sprintf("%d 个附件", len(options.Attachments))
	}
	return ""
}

func randomID(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	return hex.EncodeToString(buf)[:n]
}
