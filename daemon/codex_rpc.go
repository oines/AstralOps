package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"time"
)

func (c *codexClient) request(method string, params any, timeout time.Duration) (json.RawMessage, error) {
	id := atomic.AddInt64(&c.nextID, 1)
	ch := make(chan codexRPCResponse, 1)

	c.mu.Lock()
	if c.stdin == nil {
		c.mu.Unlock()
		return nil, errors.New("codex app-server stdin is not open")
	}
	c.pending[id] = ch
	c.mu.Unlock()

	if err := c.writeJSON(map[string]any{"id": id, "method": method, "params": params}); err != nil {
		c.forgetRequest(id)
		return nil, err
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case response := <-ch:
		if response.Error != nil {
			return nil, errors.New(response.Error.Message)
		}
		return response.Result, nil
	case <-timer.C:
		c.forgetRequest(id)
		return nil, fmt.Errorf("codex %s timed out", method)
	case <-c.closed:
		c.forgetRequest(id)
		return nil, errors.New("codex app-server exited")
	}
}

func (c *codexClient) notify(method string, params any) error {
	msg := map[string]any{"method": method}
	if params != nil {
		msg["params"] = params
	}
	return c.writeJSON(msg)
}

func (c *codexClient) respondApproval(approvalID string, response map[string]any) error {
	c.mu.Lock()
	pending, ok := c.approvals[approvalID]
	if ok {
		delete(c.approvals, approvalID)
	}
	c.mu.Unlock()
	if !ok {
		return fmt.Errorf("approval %s not found", approvalID)
	}

	result, err := codexApprovalResponse(pending.Method, response)
	if err != nil {
		return c.writeJSON(map[string]any{"id": pending.RequestID, "error": map[string]any{"code": -32000, "message": err.Error()}})
	}
	return c.writeJSON(map[string]any{"id": pending.RequestID, "result": result})
}

func (c *codexClient) writeJSON(payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stdin == nil {
		return errors.New("codex app-server stdin is not open")
	}
	_, err = c.stdin.Write(append(body, '\n'))
	return err
}

func (c *codexClient) deliverResponse(id int64, response codexRPCResponse) bool {
	c.mu.Lock()
	ch, ok := c.pending[id]
	if ok {
		delete(c.pending, id)
	}
	c.mu.Unlock()
	if ok {
		ch <- response
	}
	return ok
}

func (c *codexClient) forgetRequest(id int64) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

func numericID(value any) (int64, bool) {
	switch v := value.(type) {
	case float64:
		return int64(v), true
	case int64:
		return v, true
	case int:
		return int64(v), true
	default:
		return 0, false
	}
}

func numberValue(value any) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case int64:
		return float64(v)
	default:
		return 0
	}
}
