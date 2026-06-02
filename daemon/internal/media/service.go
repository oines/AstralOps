package media

import "github.com/oines/astralops/pkg/protocol"

type Store interface {
	DataDir() string
	GetSession(id string) (protocol.Session, bool)
	QueryEvents(workspaceID, sessionID string, afterSeq int64) []protocol.AstralEvent
}

type StreamWriter interface {
	WriteMedia(frameType string, frame *protocol.MediaStreamFrame)
}

type Service struct {
	store Store
}

func New(store Store) *Service {
	return &Service{store: store}
}
