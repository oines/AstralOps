package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	daemonLogMaxBytes = 5 * 1024 * 1024
	daemonLogBackups  = 4
	daemonLogFileName = "daemon.txt"
)

var daemonLogRedactions = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(Authorization:\s*Bearer\s+)[^\s"']+`),
	regexp.MustCompile(`(?i)(Bearer\s+)[A-Za-z0-9._~+/-]{16,}`),
	regexp.MustCompile(`(ASTRALOPS_TOKEN=)[^\s"']+`),
	regexp.MustCompile(`(?i)(["']?(?:token|account_token|private_key|password)["']?\s*[:=]\s*["']?)[^"',\s}]+`),
}

var daemonDiagnosticLog = struct {
	sync.Mutex
	enabled bool
	file    *os.File
}{}

func setupDaemonLogging(dataDir string, enabled bool) error {
	return configureDaemonDiagnosticLogging(dataDir, enabled, true)
}

func configureDaemonDiagnosticLogging(dataDir string, enabled bool, reset bool) error {
	daemonDiagnosticLog.Lock()
	defer daemonDiagnosticLog.Unlock()

	if !enabled {
		if daemonDiagnosticLog.enabled {
			log.Print("diagnostic logging disabled")
		}
		if daemonDiagnosticLog.file != nil {
			_ = daemonDiagnosticLog.file.Close()
			daemonDiagnosticLog.file = nil
		}
		daemonDiagnosticLog.enabled = false
		log.SetOutput(os.Stderr)
		log.SetFlags(log.LstdFlags)
		return nil
	}
	if daemonDiagnosticLog.enabled && daemonDiagnosticLog.file != nil && !reset {
		return nil
	}
	if daemonDiagnosticLog.file != nil {
		_ = daemonDiagnosticLog.file.Close()
		daemonDiagnosticLog.file = nil
	}
	dir := filepath.Join(dataDir, "logs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(dir, daemonLogFileName)
	if reset {
		startNewDaemonLogFile(path)
	} else {
		rotateDaemonLogFile(path)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	log.SetOutput(io.MultiWriter(scrubLogWriter{writer: os.Stderr}, scrubLogWriter{writer: file}))
	log.SetFlags(log.LstdFlags | log.Lmicroseconds | log.LUTC)
	daemonDiagnosticLog.file = file
	daemonDiagnosticLog.enabled = true
	log.Print("diagnostic logging enabled")
	return nil
}

func daemonDiagnosticLoggingEnabled() bool {
	daemonDiagnosticLog.Lock()
	defer daemonDiagnosticLog.Unlock()
	return daemonDiagnosticLog.enabled
}

func rotateDaemonLogFile(path string) {
	info, err := os.Stat(path)
	if err != nil || info.Size() < daemonLogMaxBytes {
		return
	}
	for index := daemonLogBackups - 1; index >= 1; index-- {
		from := backupDaemonLogPath(path, index)
		to := backupDaemonLogPath(path, index+1)
		if _, err := os.Stat(to); err == nil {
			_ = os.Remove(to)
		}
		if _, err := os.Stat(from); err == nil {
			_ = os.Rename(from, to)
		}
	}
	_ = os.Remove(backupDaemonLogPath(path, 1))
	_ = os.Rename(path, backupDaemonLogPath(path, 1))
}

func startNewDaemonLogFile(path string) {
	for index := daemonLogBackups - 1; index >= 1; index-- {
		from := backupDaemonLogPath(path, index)
		to := backupDaemonLogPath(path, index+1)
		if _, err := os.Stat(to); err == nil {
			_ = os.Remove(to)
		}
		if _, err := os.Stat(from); err == nil {
			_ = os.Rename(from, to)
		}
	}
	if _, err := os.Stat(path); err == nil {
		_ = os.Remove(backupDaemonLogPath(path, 1))
		_ = os.Rename(path, backupDaemonLogPath(path, 1))
	}
	_ = os.WriteFile(path, nil, 0o600)
}

func backupDaemonLogPath(path string, index int) string {
	ext := filepath.Ext(path)
	base := strings.TrimSuffix(path, ext)
	return base + "." + strconv.Itoa(index) + ext
}

type scrubLogWriter struct {
	writer io.Writer
}

func (w scrubLogWriter) Write(p []byte) (int, error) {
	text := string(p)
	for _, pattern := range daemonLogRedactions {
		text = pattern.ReplaceAllString(text, `${1}[redacted]`)
	}
	_, err := w.writer.Write([]byte(text))
	return len(p), err
}

func logControlActionStart(req ControlRequest) time.Time {
	if !daemonDiagnosticLoggingEnabled() {
		return time.Time{}
	}
	startedAt := time.Now()
	log.Printf("control action start action=%q capability=%q controller_device_id=%q request_id=%q params=%s", req.Action, req.Capability, req.ControllerDeviceID, req.RequestID, safeControlParamsJSON(req.Action, req.Params))
	return startedAt
}

func logControlActionCompleted(req ControlRequest, startedAt time.Time) {
	if startedAt.IsZero() || !daemonDiagnosticLoggingEnabled() {
		return
	}
	log.Printf("control action completed action=%q capability=%q controller_device_id=%q request_id=%q duration_ms=%d", req.Action, req.Capability, req.ControllerDeviceID, req.RequestID, time.Since(startedAt).Milliseconds())
}

func logControlActionFailed(req ControlRequest, startedAt time.Time, err error) {
	if startedAt.IsZero() || !daemonDiagnosticLoggingEnabled() {
		return
	}
	code := ""
	status := 0
	var actionErr *actionError
	if errors.As(err, &actionErr) && actionErr != nil {
		code = string(actionErr.Code)
		status = actionErr.Status
	}
	log.Printf("control action failed action=%q capability=%q controller_device_id=%q request_id=%q duration_ms=%d status=%d code=%q error=%q", req.Action, req.Capability, req.ControllerDeviceID, req.RequestID, time.Since(startedAt).Milliseconds(), status, code, err)
}

type diagnosticStatusRecorder struct {
	http.ResponseWriter
	status int
}

func (w *diagnosticStatusRecorder) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *diagnosticStatusRecorder) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(body)
}

func (a *app) diagnosticHTTPLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !daemonDiagnosticLoggingEnabled() || r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		startedAt := time.Now()
		if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") || r.URL.Query().Get("stream") == "1" || strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
			log.Printf("http stream start method=%q path=%q query=%s", r.Method, r.URL.Path, safeHTTPQueryJSON(r))
			next.ServeHTTP(w, r)
			log.Printf("http stream completed method=%q path=%q duration_ms=%d", r.Method, r.URL.Path, time.Since(startedAt).Milliseconds())
			return
		}
		recorder := &diagnosticStatusRecorder{ResponseWriter: w}
		log.Printf("http request start method=%q path=%q query=%s", r.Method, r.URL.Path, safeHTTPQueryJSON(r))
		next.ServeHTTP(recorder, r)
		status := recorder.status
		if status == 0 {
			status = http.StatusOK
		}
		log.Printf("http request completed method=%q path=%q status=%d duration_ms=%d", r.Method, r.URL.Path, status, time.Since(startedAt).Milliseconds())
	})
}

func safeHTTPQueryJSON(r *http.Request) string {
	out := map[string]any{}
	allowed := map[string]bool{
		"workspace_id": true,
		"session_id":   true,
		"after_seq":    true,
		"before_seq":   true,
		"limit":        true,
		"path":         true,
		"stream":       true,
		"discover":     true,
	}
	for key, values := range r.URL.Query() {
		if !allowed[key] || len(values) == 0 {
			continue
		}
		out[key] = values[0]
	}
	body, err := json.Marshal(out)
	if err != nil {
		return "{}"
	}
	return string(body)
}

func logDiagnosticEvent(event AstralEvent) {
	if !daemonDiagnosticLoggingEnabled() || !diagnosticEventVisible(string(event.Kind)) {
		return
	}
	log.Printf("event emitted seq=%d kind=%q workspace_id=%q session_id=%q summary=%s", event.Seq, event.Kind, event.WorkspaceID, event.SessionID, diagnosticEventSummaryJSON(event))
}

func diagnosticEventVisible(kind string) bool {
	if strings.HasPrefix(kind, "message.") || strings.HasPrefix(kind, "reasoning.") || strings.HasPrefix(kind, "tool.") {
		return false
	}
	switch kind {
	case "control.context", "control.rate_limit", "control.notification":
		return false
	default:
		return true
	}
}

func diagnosticEventSummaryJSON(event AstralEvent) string {
	value := diagnosticEventNormalizedMap(event)
	out := map[string]any{}
	copyStringParam(out, value, "status")
	copyStringParam(out, value, "reason")
	copyStringParam(out, value, "message")
	copyStringParam(out, value, "helper_status")
	copyStringParam(out, value, "workspace_id")
	copyStringParam(out, value, "session_id")
	copyStringParam(out, value, "target")
	copyStringParam(out, value, "agent")
	copyStringParam(out, value, "endpoint")
	copyStringParam(out, value, "remote_cwd")
	copyStringParam(out, value, "remote_user")
	copyStringParam(out, value, "remote_host")
	copyStringParam(out, value, "remote_os")
	copyStringParam(out, value, "remote_arch")
	copyStringParam(out, value, "terminal_id")
	copyStringParam(out, value, "queue_id")
	copyStringParam(out, value, "approval_id")
	copyStringParam(out, value, "ask_id")
	copyNumberParam(out, value, "port")
	copyNumberParam(out, value, "retry_attempt")
	copyNumberParam(out, value, "retry_max")
	copyNumberParam(out, value, "exit_code")
	body, err := json.Marshal(out)
	if err != nil {
		return "{}"
	}
	return string(body)
}

func diagnosticEventNormalizedMap(event AstralEvent) map[string]any {
	return mapValue(event.Normalized)
}

func logSSHProxyCallStart(workspace Workspace, method string, params any) time.Time {
	if !daemonDiagnosticLoggingEnabled() {
		return time.Time{}
	}
	startedAt := time.Now()
	log.Printf("ssh proxy call start workspace_id=%q method=%q params=%s", workspace.ID, method, safeSSHProxyParamsJSON(method, params))
	return startedAt
}

func logSSHProxyCallCompleted(workspace Workspace, method string, startedAt time.Time) {
	if startedAt.IsZero() || !daemonDiagnosticLoggingEnabled() {
		return
	}
	log.Printf("ssh proxy call completed workspace_id=%q method=%q duration_ms=%d", workspace.ID, method, time.Since(startedAt).Milliseconds())
}

func logSSHProxyCallFailed(workspace Workspace, method string, startedAt time.Time, err error) {
	if startedAt.IsZero() || !daemonDiagnosticLoggingEnabled() {
		return
	}
	log.Printf("ssh proxy call failed workspace_id=%q method=%q duration_ms=%d transport=%t error=%q", workspace.ID, method, time.Since(startedAt).Milliseconds(), isProxyTransportError(err), err)
}

func logDiagnosticSpanStart(name string, fields map[string]any) time.Time {
	if !daemonDiagnosticLoggingEnabled() {
		return time.Time{}
	}
	startedAt := time.Now()
	log.Printf("diagnostic span start name=%q fields=%s", name, safeDiagnosticFieldsJSON(fields))
	return startedAt
}

func logDiagnosticSpanCompleted(name string, startedAt time.Time, fields map[string]any) {
	if startedAt.IsZero() || !daemonDiagnosticLoggingEnabled() {
		return
	}
	withDuration := copyDiagnosticFields(fields)
	withDuration["duration_ms"] = time.Since(startedAt).Milliseconds()
	log.Printf("diagnostic span completed name=%q fields=%s", name, safeDiagnosticFieldsJSON(withDuration))
}

func logDiagnosticSpanFailed(name string, startedAt time.Time, err error, fields map[string]any) {
	if startedAt.IsZero() || !daemonDiagnosticLoggingEnabled() {
		return
	}
	withDuration := copyDiagnosticFields(fields)
	withDuration["duration_ms"] = time.Since(startedAt).Milliseconds()
	if err != nil {
		withDuration["error"] = err.Error()
	}
	log.Printf("diagnostic span failed name=%q fields=%s", name, safeDiagnosticFieldsJSON(withDuration))
}

func copyDiagnosticFields(fields map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range fields {
		out[key] = value
	}
	return out
}

func safeDiagnosticFieldsJSON(fields map[string]any) string {
	if len(fields) == 0 {
		return "{}"
	}
	out := map[string]any{}
	for key, value := range fields {
		out[key] = safeDiagnosticFieldValue(key, value)
	}
	body, err := json.Marshal(out)
	if err != nil {
		return "{}"
	}
	return string(body)
}

func safeDiagnosticFieldValue(key string, value any) any {
	lower := strings.ToLower(key)
	if strings.Contains(lower, "token") || strings.Contains(lower, "password") || strings.Contains(lower, "private_key") || strings.Contains(lower, "authorization") {
		return "[redacted]"
	}
	switch typed := value.(type) {
	case nil:
		return nil
	case string:
		return diagnosticLogString(typed)
	case bool:
		return typed
	case int:
		return typed
	case int64:
		return typed
	case float64:
		return typed
	case map[string]any:
		out := map[string]any{}
		for childKey, childValue := range typed {
			out[childKey] = safeDiagnosticFieldValue(childKey, childValue)
		}
		return out
	default:
		return diagnosticLogString(fmt.Sprint(typed))
	}
}

func diagnosticLogString(value string) string {
	value = strings.TrimSpace(value)
	const max = 2048
	if len(value) <= max {
		return value
	}
	return value[:max] + "...[truncated]"
}

func diagnosticLogTail(value string) string {
	value = strings.TrimSpace(value)
	const max = 2048
	if len(value) <= max {
		return value
	}
	return "[truncated]..." + value[len(value)-max:]
}

func safeSSHProxyParamsJSON(method string, params any) string {
	value := mapValue(params)
	out := map[string]any{"method": method}
	copyStringParam(out, value, "id")
	copyStringParam(out, value, "path")
	copyStringParam(out, value, "cwd")
	copyStringParam(out, value, "from")
	copyStringParam(out, value, "to")
	copyStringParam(out, value, "source")
	copyStringParam(out, value, "destination")
	copyStringParam(out, value, "remote_path")
	copyStringParam(out, value, "target")
	copyNumberParam(out, value, "offset")
	copyNumberParam(out, value, "limit")
	copyNumberParam(out, value, "cols")
	copyNumberParam(out, value, "rows")
	for _, key := range []string{"command", "data", "content", "body", "patch"} {
		if text := stringValue(value[key]); text != "" {
			out[key+"_length"] = len(text)
		}
	}
	body, err := json.Marshal(out)
	if err != nil {
		return "{}"
	}
	return string(body)
}

func safeControlParamsJSON(action ControlAction, params json.RawMessage) string {
	if len(params) == 0 {
		return "{}"
	}
	var value map[string]any
	if err := json.Unmarshal(params, &value); err != nil {
		return "{}"
	}
	body, err := json.Marshal(safeControlParams(action, value))
	if err != nil {
		return "{}"
	}
	return string(body)
}

func safeControlParams(action ControlAction, params map[string]any) map[string]any {
	out := map[string]any{}
	copyStringParam(out, params, "workspace_id")
	copyStringParam(out, params, "session_id")
	copyStringParam(out, params, "queue_id")
	copyStringParam(out, params, "approval_id")
	copyStringParam(out, params, "ask_id")
	copyStringParam(out, params, "stream_id")
	copyStringParam(out, params, "terminal_id")
	copyStringParam(out, params, "request_id")
	copyStringParam(out, params, "media_id")
	copyStringParam(out, params, "path")
	copyStringParam(out, params, "from")
	copyStringParam(out, params, "to")
	copyStringParam(out, params, "root")
	copyStringParam(out, params, "cwd")
	copyNumberParam(out, params, "event_seq")
	copyNumberParam(out, params, "before_seq")
	copyNumberParam(out, params, "after_seq")
	copyNumberParam(out, params, "limit")
	copyNumberParam(out, params, "offset")
	copyNumberParam(out, params, "chunk_size")
	copyNumberParam(out, params, "cols")
	copyNumberParam(out, params, "rows")

	switch action {
	case ControlActionWorkspaceCreate:
		copyStringParam(out, params, "name")
		copyStringParam(out, params, "target")
		copyStringParam(out, params, "agent")
		copyStringParam(out, params, "local_cwd")
		if ssh := mapValue(params["ssh"]); len(ssh) > 0 {
			out["ssh"] = map[string]any{
				"endpoint":   stringValue(ssh["endpoint"]),
				"port":       numberLogValue(ssh["port"]),
				"remote_cwd": stringValue(ssh["remote_cwd"]),
			}
		}
	case ControlActionSessionInput, ControlActionSessionEdit:
		if input := stringValue(params["input"]); input != "" {
			out["input_length"] = len(input)
		}
		copyStringParam(out, params, "model")
		copyStringParam(out, params, "reasoning_effort")
		copyStringParam(out, params, "permission_mode")
		if attachments, ok := params["attachments"].([]any); ok {
			out["attachment_count"] = len(attachments)
		}
	case ControlActionWorkspaceExec:
		if command := stringValue(params["command"]); command != "" {
			out["command_present"] = true
			out["command_length"] = len(command)
		}
	case ControlActionTerminalInput:
		if data := stringValue(params["data"]); data != "" {
			out["input_bytes"] = len(data)
		}
	case ControlActionAttachmentIngest, ControlActionAttachmentIngestStart, ControlActionAttachmentIngestFinish:
		copyStringParam(out, params, "name")
		copyStringParam(out, params, "mime_type")
		copyNumberParam(out, params, "size")
	case ControlActionAttachmentIngestChunk:
		copyStringParam(out, params, "upload_id")
		copyNumberParam(out, params, "offset")
		if data := stringValue(params["data_base64"]); data != "" {
			out["data_base64_length"] = len(data)
		}
	}
	return out
}

func copyStringParam(out map[string]any, params map[string]any, key string) {
	if value := stringValue(params[key]); value != "" {
		out[key] = value
	}
}

func copyNumberParam(out map[string]any, params map[string]any, key string) {
	if value, ok := intLikeValue(params[key]); ok {
		out[key] = value
	}
}

func numberLogValue(value any) any {
	if number, ok := intLikeValue(value); ok {
		return number
	}
	return nil
}
