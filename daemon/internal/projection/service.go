package projection

import (
	"encoding/json"
	"sort"
	"strconv"
	"sync"

	"github.com/oines/astralops/pkg/protocol"
)

type ClaudeSlashCommandsFunc func(protocol.AstralEvent) []string

type Options struct {
	ClaudeSlashCommands ClaudeSlashCommandsFunc
}

type Service struct {
	mu                  sync.Mutex
	sessions            map[string]sessionProjection
	sessionViews        map[string]cachedSessionView
	applied             map[string]bool
	claudeSlashCommands ClaudeSlashCommandsFunc
}

type sessionProjection struct {
	LatestContext       map[string]any
	ContextCompacted    bool
	ClaudeSlashCommands []string
}

type cachedSessionView struct {
	key  string
	view protocol.SessionView
}

type SessionViewBuilder func() (protocol.SessionView, bool)

func New(options Options) *Service {
	return &Service{
		sessions:            map[string]sessionProjection{},
		sessionViews:        map[string]cachedSessionView{},
		applied:             map[string]bool{},
		claudeSlashCommands: options.ClaudeSlashCommands,
	}
}

func (s *Service) Apply(ev protocol.AstralEvent) {
	if s == nil || ev.SessionID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessions == nil {
		s.sessions = map[string]sessionProjection{}
	}
	if s.sessionViews == nil {
		s.sessionViews = map[string]cachedSessionView{}
	}
	if s.applied == nil {
		s.applied = map[string]bool{}
	}
	key := eventKey(ev)
	if key != "" {
		if s.applied[key] {
			return
		}
		s.applied[key] = true
	}
	delete(s.sessionViews, ev.SessionID)
	projection := s.sessions[ev.SessionID]
	switch ev.Kind {
	case "control.context":
		if value := mapValue(ev.Normalized); len(value) > 0 {
			projection.LatestContext = mergeProjectedContext(projection.LatestContext, value, projection.ContextCompacted)
			if !(projection.ContextCompacted && compactedContextShouldStayInvalid(value)) {
				projection.ContextCompacted = false
			}
		}
	case "memory.compacted":
		projection.LatestContext = nil
		projection.ContextCompacted = true
	case "session.native":
		if s.claudeSlashCommands != nil {
			if commands := s.claudeSlashCommands(ev); len(commands) > 0 {
				projection.ClaudeSlashCommands = append([]string(nil), commands...)
			}
		}
	}
	s.sessions[ev.SessionID] = projection
}

func (s *Service) Replay(events []protocol.AstralEvent) {
	if s == nil {
		return
	}
	ordered := append([]protocol.AstralEvent(nil), events...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Seq == ordered[j].Seq {
			leftTS := ordered[i].TS
			rightTS := ordered[j].TS
			if leftTS != "" && rightTS != "" && leftTS != rightTS {
				return leftTS < rightTS
			}
			return ordered[i].Kind < ordered[j].Kind
		}
		return ordered[i].Seq < ordered[j].Seq
	})
	s.mu.Lock()
	s.sessions = map[string]sessionProjection{}
	s.sessionViews = map[string]cachedSessionView{}
	s.applied = map[string]bool{}
	s.mu.Unlock()
	for _, ev := range ordered {
		s.Apply(ev)
	}
}

func (s *Service) SessionView(sessionID string, key string, builder SessionViewBuilder) (protocol.SessionView, bool) {
	if s == nil || sessionID == "" {
		return protocol.SessionView{}, false
	}
	s.mu.Lock()
	if s.sessionViews != nil {
		if cached, ok := s.sessionViews[sessionID]; ok && cached.key == key {
			s.mu.Unlock()
			return cached.view, true
		}
	}
	s.mu.Unlock()
	if builder == nil {
		return protocol.SessionView{}, false
	}
	view, ok := builder()
	if !ok {
		return protocol.SessionView{}, false
	}
	s.mu.Lock()
	if s.sessionViews == nil {
		s.sessionViews = map[string]cachedSessionView{}
	}
	s.sessionViews[sessionID] = cachedSessionView{key: key, view: view}
	s.mu.Unlock()
	return view, true
}

func (s *Service) LatestContext(sessionID string) map[string]any {
	if s == nil || sessionID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if value := s.sessions[sessionID].LatestContext; len(value) > 0 {
		return copyStringAny(value)
	}
	return nil
}

func (s *Service) ClaudeSlashCommands(sessionID string) []string {
	if s == nil || sessionID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	values := s.sessions[sessionID].ClaudeSlashCommands
	if len(values) == 0 {
		return nil
	}
	out := make([]string, len(values))
	copy(out, values)
	return out
}

func eventKey(ev protocol.AstralEvent) string {
	if ev.Seq <= 0 && ev.TS == "" {
		return ""
	}
	return ev.WorkspaceID + "\x00" + ev.SessionID + "\x00" + string(ev.Kind) + "\x00" + strconv.FormatInt(ev.Seq, 10) + "\x00" + ev.TS
}

func mergeProjectedContext(existing map[string]any, next map[string]any, compacted bool) map[string]any {
	if len(next) == 0 {
		return existing
	}
	if compacted && compactedContextShouldStayInvalid(next) {
		return existing
	}
	if len(existing) == 0 {
		return copyStringAny(next)
	}
	scope := stringValue(next["scope"])
	existingScope := stringValue(existing["scope"])
	if scope == "aggregate" && existingScope == "current" {
		merged := copyStringAny(existing)
		copyContextFields(merged, next, []string{
			"model",
			"model_context_window",
			"model_usage",
			"cumulative_total_tokens",
			"cumulative_input_tokens",
			"cumulative_output_tokens",
			"cumulative_cached_input_tokens",
			"cumulative_cache_creation_input_tokens",
		})
		refreshProjectedContextPercent(merged)
		return merged
	}
	if scope == "current" {
		merged := copyStringAny(next)
		copyContextFields(merged, existing, []string{
			"model",
			"model_context_window",
			"model_usage",
			"cumulative_total_tokens",
			"cumulative_input_tokens",
			"cumulative_output_tokens",
			"cumulative_cached_input_tokens",
			"cumulative_cache_creation_input_tokens",
		})
		refreshProjectedContextPercent(merged)
		return merged
	}
	return copyStringAny(next)
}

func compactedContextShouldStayInvalid(value map[string]any) bool {
	return stringValue(value["scope"]) == "aggregate" || stringValue(value["source"]) == "astralops"
}

func copyContextFields(target map[string]any, source map[string]any, keys []string) {
	for _, key := range keys {
		if target[key] == nil && source[key] != nil {
			target[key] = source[key]
		}
	}
}

func refreshProjectedContextPercent(value map[string]any) {
	if percent := contextUsedPercent(value); percent > 0 {
		value["used_percent"] = percent
	}
}

func contextUsedPercent(value map[string]any) int {
	total := numberValue(firstNonNil(value["total_tokens"], value["totalTokens"]))
	window := numberValue(firstNonNil(value["model_context_window"], value["modelContextWindow"], value["context_window"], value["contextWindow"]))
	if total <= 0 || window <= 0 {
		return 0
	}
	percent := int((total / window) * 100)
	if percent < 1 {
		return 1
	}
	if percent > 999 {
		return 999
	}
	return percent
}

func firstNonNil(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func mapValue(v any) map[string]any {
	if v == nil {
		return nil
	}
	if m, ok := v.(map[string]any); ok {
		return m
	}
	body, err := json.Marshal(v)
	if err != nil || len(body) == 0 || string(body) == "null" {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		return nil
	}
	if out == nil {
		return nil
	}
	return out
}

func stringValue(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func numberValue(value any) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case json.Number:
		f, _ := v.Float64()
		return f
	default:
		return 0
	}
}

func copyStringAny(input map[string]any) map[string]any {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}
