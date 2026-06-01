package eventlog

import (
	"context"
	"log"

	"github.com/oines/astralops/pkg/protocol"
)

type Store interface {
	AppendEvent(protocol.AstralEvent) (protocol.AstralEvent, error)
	AllEvents() []protocol.AstralEvent
}

type ProjectionSink interface {
	Apply(protocol.AstralEvent)
}

type Broadcaster interface {
	Broadcast(protocol.AstralEvent)
}

type NotificationPolicy interface {
	Target(protocol.AstralEvent) (title string, sessionID string)
	Build(source protocol.AstralEvent, title string, sessionID string, events []protocol.AstralEvent) (protocol.AstralEvent, bool)
}

type DiagnosticLogger func(protocol.AstralEvent)

type Service struct {
	store         Store
	projections   ProjectionSink
	broadcaster   Broadcaster
	notifications NotificationPolicy
	diagnostics   DiagnosticLogger
}

type Options struct {
	Store         Store
	Projections   ProjectionSink
	Broadcaster   Broadcaster
	Notifications NotificationPolicy
	Diagnostics   DiagnosticLogger
}

func New(options Options) *Service {
	return &Service{
		store:         options.Store,
		projections:   options.Projections,
		broadcaster:   options.Broadcaster,
		notifications: options.Notifications,
		diagnostics:   options.Diagnostics,
	}
}

func (s *Service) Publish(_ context.Context, event protocol.AstralEvent) (protocol.AstralEvent, error) {
	if s == nil || s.store == nil {
		return protocol.AstralEvent{}, nil
	}
	saved, err := s.store.AppendEvent(event)
	if err != nil {
		log.Printf("append event: %v", err)
		return protocol.AstralEvent{}, err
	}
	s.publishSaved(saved)
	if s.notifications != nil {
		title, sessionID := s.notifications.Target(saved)
		if notification, ok := s.notifications.Build(saved, title, sessionID, s.store.AllEvents()); ok {
			savedNotification, err := s.store.AppendEvent(notification)
			if err != nil {
				log.Printf("append notification event: %v", err)
				return saved, err
			}
			s.publishSaved(savedNotification)
		}
	}
	return saved, nil
}

func (s *Service) publishSaved(event protocol.AstralEvent) {
	if s.projections != nil {
		s.projections.Apply(event)
	}
	if s.diagnostics != nil {
		s.diagnostics(event)
	}
	if s.broadcaster != nil {
		s.broadcaster.Broadcast(event)
	}
}
