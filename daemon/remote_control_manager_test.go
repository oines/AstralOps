package main

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type timeoutFrameConn struct {
	closed atomic.Bool
	writes atomic.Int32
}

func (c *timeoutFrameConn) Close() error {
	c.closed.Store(true)
	return nil
}

func (c *timeoutFrameConn) WritePlain(controlPlainFrame) error {
	c.writes.Add(1)
	return nil
}

func (c *timeoutFrameConn) ReadPlain(time.Duration) (controlPlainFrame, error) {
	return controlPlainFrame{}, errors.New("not implemented")
}

func TestManagedControlRequestTimeoutClosesCachedSession(t *testing.T) {
	st, err := loadStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	manager := newRemoteControlManager(&app{store: st})
	conn := &timeoutFrameConn{}
	session := &remoteControlManagedSession{
		manager:       manager,
		hostDeviceID:  "dev_host",
		conn:          conn,
		target:        controlClientTarget{Timeout: 5 * time.Millisecond},
		pending:       map[string]chan controlPlainFrame{},
		streams:       map[string][]chan controlPlainFrame{},
		orphanStreams: map[string][]controlPlainFrame{},
		closed:        make(chan struct{}),
	}

	_, err = session.request(context.Background(), ControlRequest{
		RequestID:  "req_timeout",
		Capability: CapabilityCoreRead,
		Action:     ControlActionWorkspaces,
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want deadline exceeded", err)
	}
	if conn.writes.Load() != 1 {
		t.Fatalf("writes = %d, want 1", conn.writes.Load())
	}
	if !conn.closed.Load() {
		t.Fatal("timed-out managed session was not closed")
	}
}
