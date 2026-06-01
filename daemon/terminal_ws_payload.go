package main

func terminalReadySocketPayload(terminalID, shell, cwd string, outputSeq int64, viewerID, inputLeaseID string, canInput bool) map[string]any {
	payload := map[string]any{
		"type":        "ready",
		"terminal_id": terminalID,
		"shell":       shell,
		"cwd":         cwd,
		"output_seq":  outputSeq,
		"can_input":   canInput,
	}
	if viewerID != "" {
		payload["viewer_id"] = viewerID
	}
	if inputLeaseID != "" {
		payload["input_lease_id"] = inputLeaseID
	}
	return payload
}

func terminalHeartbeatSocketPayload(frame *terminalStreamFrame) map[string]any {
	if frame == nil || frame.ViewerID == "" || frame.InputLeaseID == "" {
		return nil
	}
	return map[string]any{
		"type":           "heartbeat",
		"terminal_id":    frame.TerminalID,
		"viewer_id":      frame.ViewerID,
		"input_lease_id": frame.InputLeaseID,
		"heartbeat_seq":  frame.HeartbeatSeq,
		"output_seq":     frame.OutputSeq,
		"can_input":      frame.CanInput,
	}
}

func terminalOutputSocketPayload(frame *terminalStreamFrame) map[string]any {
	if frame == nil || frame.Data == "" {
		return nil
	}
	return map[string]any{
		"type":       "output",
		"data":       frame.Data,
		"output_seq": frame.OutputSeq,
		"can_input":  frame.CanInput,
	}
}

func terminalExitSocketPayload(frame *terminalStreamFrame) map[string]any {
	payload := map[string]any{"type": "exit"}
	if frame == nil {
		return payload
	}
	payload["reason"] = frame.Reason
	payload["output_seq"] = frame.OutputSeq
	return payload
}
