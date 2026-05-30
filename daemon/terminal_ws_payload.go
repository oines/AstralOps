package main

func terminalReadySocketPayload(terminalID, shell, cwd string, outputSeq int64) map[string]any {
	return map[string]any{
		"type":        "ready",
		"terminal_id": terminalID,
		"shell":       shell,
		"cwd":         cwd,
		"output_seq":  outputSeq,
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
