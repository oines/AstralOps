package ssh

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	pathpkg "path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/oines/astralops/pkg/protocol"
)

type Diagnostics struct {
	Enabled            func() bool
	SpanStart          func(string, map[string]any) time.Time
	SpanCompleted      func(string, time.Time, map[string]any)
	SpanFailed         func(string, time.Time, error, map[string]any)
	ProxyCallStart     func(protocol.Workspace, string, any) time.Time
	ProxyCallCompleted func(protocol.Workspace, string, time.Time)
	ProxyCallFailed    func(protocol.Workspace, string, time.Time, error)
	LogTail            func(string) string
	CopyFields         func(map[string]any) map[string]any
}

var diagnosticsState struct {
	sync.RWMutex
	hooks Diagnostics
}

func ConfigureDiagnostics(hooks Diagnostics) {
	diagnosticsState.Lock()
	diagnosticsState.hooks = hooks
	diagnosticsState.Unlock()
}

func currentDiagnostics() Diagnostics {
	diagnosticsState.RLock()
	defer diagnosticsState.RUnlock()
	return diagnosticsState.hooks
}

func daemonDiagnosticLoggingEnabled() bool {
	if enabled := currentDiagnostics().Enabled; enabled != nil {
		return enabled()
	}
	return false
}

func logDiagnosticSpanStart(name string, fields map[string]any) time.Time {
	if hook := currentDiagnostics().SpanStart; hook != nil {
		return hook(name, fields)
	}
	return time.Time{}
}

func logDiagnosticSpanCompleted(name string, startedAt time.Time, fields map[string]any) {
	if hook := currentDiagnostics().SpanCompleted; hook != nil {
		hook(name, startedAt, fields)
	}
}

func logDiagnosticSpanFailed(name string, startedAt time.Time, err error, fields map[string]any) {
	if hook := currentDiagnostics().SpanFailed; hook != nil {
		hook(name, startedAt, err, fields)
	}
}

func logSSHProxyCallStart(workspace protocol.Workspace, method string, params any) time.Time {
	if hook := currentDiagnostics().ProxyCallStart; hook != nil {
		return hook(workspace, method, params)
	}
	return time.Time{}
}

func logSSHProxyCallCompleted(workspace protocol.Workspace, method string, startedAt time.Time) {
	if hook := currentDiagnostics().ProxyCallCompleted; hook != nil {
		hook(workspace, method, startedAt)
	}
}

func logSSHProxyCallFailed(workspace protocol.Workspace, method string, startedAt time.Time, err error) {
	if hook := currentDiagnostics().ProxyCallFailed; hook != nil {
		hook(workspace, method, startedAt, err)
	}
}

func copyDiagnosticFields(fields map[string]any) map[string]any {
	if hook := currentDiagnostics().CopyFields; hook != nil {
		return hook(fields)
	}
	out := map[string]any{}
	for key, value := range fields {
		out[key] = value
	}
	return out
}

func diagnosticLogTail(value string) string {
	if hook := currentDiagnostics().LogTail; hook != nil {
		return hook(value)
	}
	value = strings.TrimSpace(value)
	if len(value) <= 4096 {
		return value
	}
	return value[len(value)-4096:]
}

func mapValue(v any) map[string]any {
	switch value := v.(type) {
	case map[string]any:
		return value
	case protocol.AstralEventNormalized:
		return protocol.NormalizedMap(value)
	default:
		return nil
	}
}

func stringValue(v any) string {
	switch value := v.(type) {
	case string:
		return value
	case fmt.Stringer:
		return value.String()
	case json.Number:
		return value.String()
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64)
	case int:
		return strconv.Itoa(value)
	default:
		return ""
	}
}

func firstString(values ...any) string {
	for _, value := range values {
		if text := strings.TrimSpace(stringValue(value)); text != "" {
			return text
		}
	}
	return ""
}

func randomID(n int) string {
	if n <= 0 {
		return ""
	}
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)[:n]
}

func remotePathClean(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	cleaned := pathpkg.Clean(strings.ReplaceAll(value, "\\", "/"))
	if value == "/" {
		return "/"
	}
	return cleaned
}

func remotePathIsAbs(value string) bool {
	return strings.HasPrefix(remotePathClean(value), "/")
}

func remotePathJoin(parts ...string) string {
	return pathpkg.Join(parts...)
}

func remotePathDir(value string) string {
	cleaned := remotePathClean(value)
	if cleaned == "" {
		return ""
	}
	return pathpkg.Dir(cleaned)
}
