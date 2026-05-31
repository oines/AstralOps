package mobilecore

import (
	"encoding/json"
	"errors"
	"sync"

	"github.com/oines/astralops/pkg/controllercore"
)

type Callback interface {
	OnHostState(payload string)
	OnWorkbenchPatch(payload string)
	OnEvents(payload string)
	OnTerminalFrame(payload string)
	OnError(payload string)
}

type Core struct {
	mu       sync.Mutex
	callback Callback
	started  bool
}

func New() *Core {
	return &Core{}
}

func (c *Core) SetCallback(callback Callback) {
	c.mu.Lock()
	c.callback = callback
	c.mu.Unlock()
}

func (c *Core) Start(configJSON string) (string, error) {
	c.mu.Lock()
	c.started = true
	c.mu.Unlock()
	return encode(map[string]any{
		"ok":      true,
		"started": true,
		"note":    "mobile Go Controller Core bridge is initialized; transport adapters are not wired yet",
	})
}

func (c *Core) SetCloudSession(sessionJSON string) (string, error) {
	return c.unavailable()
}

func (c *Core) RefreshMesh() (string, error) {
	return c.unavailable()
}

func (c *Core) RequestPairing(hostDeviceID string) (string, error) {
	return c.unavailable()
}

func (c *Core) OpenHostSession(hostDeviceID string) (string, error) {
	return encode(controllercore.HostSessionState{
		HostDeviceID: hostDeviceID,
		State:        controllercore.StateFailed,
		CanRequest:   false,
		Workbench:    controllercore.WorkbenchStatus{State: controllercore.WorkbenchFailed, LastError: "mobile transport adapters are not wired yet"},
		Terminals:    map[string]controllercore.TerminalState{},
	})
}

func (c *Core) Snapshot(hostDeviceID, optionsJSON string) (string, error) {
	return c.unavailable()
}

func (c *Core) SendInput(hostDeviceID, sessionID, inputJSON string) (string, error) {
	return c.unavailable()
}

func (c *Core) OpenTerminal(hostDeviceID, workspaceID string) (string, error) {
	return c.unavailable()
}

func (c *Core) AttachTerminal(hostDeviceID, terminalID string, afterSeq int64) (string, error) {
	return c.unavailable()
}

func (c *Core) TerminalInput(hostDeviceID, terminalID, data string) (string, error) {
	return c.unavailable()
}

func (c *Core) TerminalResize(hostDeviceID, terminalID string, cols, rows int) (string, error) {
	return c.unavailable()
}

func (c *Core) TerminalClose(hostDeviceID, terminalID string) (string, error) {
	return c.unavailable()
}

func (c *Core) CloseHostSession(hostDeviceID string) (string, error) {
	return encode(map[string]any{"ok": true, "host_device_id": hostDeviceID})
}

func (c *Core) unavailable() (string, error) {
	err := errors.New("mobile Go Controller Core transport adapters are not wired yet")
	c.emitError(err)
	return "", err
}

func (c *Core) emitError(err error) {
	c.mu.Lock()
	callback := c.callback
	c.mu.Unlock()
	if callback == nil || err == nil {
		return
	}
	payload, _ := encode(map[string]any{"error": err.Error()})
	callback.OnError(payload)
}

func encode(value any) (string, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(body), nil
}
