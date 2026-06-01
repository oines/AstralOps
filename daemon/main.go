package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/oines/astralops/daemon/internal/eventlog"
	"github.com/oines/astralops/pkg/controllercore"
	"github.com/oines/astralops/pkg/hostcore"
)

var version = "dev"

type app struct {
	store                *store
	settings             *settingsStore
	token                string
	addr                 string
	runtimePort          int
	hub                  *eventHub
	eventLog             *eventlog.Service
	upgrader             websocket.Upgrader
	agents               map[AgentKind]agentInfo
	runtimes             map[AgentKind]AgentRuntime
	ssh                  *sshManager
	projections          *sessionProjectionCache
	controlHelloLimit    int64
	controlFrameLimit    int64
	controlMu            sync.Mutex
	controlSessions      map[string]*controlWSConn
	controlRelaySessions map[string]*controlRelaySession
	terminalMu           sync.Mutex
	terminals            *terminalManager
	queueMu              sync.Mutex
	queues               map[string][]queuedTurn
	codexExecMu          sync.Mutex
	codexExec            map[string]codexExecCommand
	codexRemoteHomeMu    sync.Mutex
	codexRemoteHome      map[string]string
	remoteManager        *remoteControlManager
	hostRemoteSessions   *hostRemoteSessionManager
	network              *networkMonitor
	remoteControlMu      sync.Mutex
	remoteControl        *remoteControlRuntime
	controllerCore       *controllercore.Controller
	controllerTransport  *controllercore.ManagedTransport
	hostCore             *hostcore.Core
	role                 appRole
	mesh                 *meshStateManager
	cloudMu              sync.Mutex
	cloudCancel          context.CancelFunc
	cloudSettings        CloudSettings
	cloudSelfRevoked     bool
	cloudRelayConnected  bool
	cloudAuthMu          sync.Mutex
	cloudAuthStates      map[string]cloudAuthState
}

func main() {
	if runClaudeRemoteMCPHelper(os.Args[1:]) {
		return
	}
	if runControlDevClient(os.Args[1:]) {
		return
	}

	dataDir := defaultDataDir()
	if err := os.MkdirAll(filepath.Join(dataDir, "runtime"), 0o700); err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dataDir, "logs"), 0o700); err != nil {
		log.Fatal(err)
	}

	st, err := loadStore(dataDir)
	if err != nil {
		log.Fatal(err)
	}
	settings, err := loadSettingsStore(dataDir)
	if err != nil {
		log.Fatal(err)
	}
	if err := setupDaemonLogging(dataDir, settings.get().Diagnostics.LoggingEnabled); err != nil {
		log.Fatal(err)
	}
	if settings.get().Diagnostics.LoggingEnabled {
		log.Printf("daemon starting data_dir=%q version=%q", dataDir, version)
	}

	token := randomToken()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port

	a := &app{
		store:       st,
		settings:    settings,
		token:       token,
		addr:        localTCPHostPort(ln.Addr().String()),
		runtimePort: port,
		hub:         newEventHub(),
		agents:      discoverAgents(),
		projections: newSessionProjectionCache(),
		queues:      map[string][]queuedTurn{},
		codexExec:   map[string]codexExecCommand{},
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		},
	}
	a.eventPublisher()
	a.remoteManager = newRemoteControlManager(remoteControlDepsFromApp(a))
	a.controllerCore = a.newControllerCore()
	a.hostRemoteSessions = newHostRemoteSessionManager(hostRemoteSessionDepsFromApp(a), a.remoteManager)
	a.network = newNetworkMonitor(networkMonitorDepsFromApp(a))
	a.mesh = newMeshStateManager(meshStateDepsFromApp(a))
	a.rebuildSessionProjections()
	if err := a.backfillHistoricalContextEvents(); err != nil {
		log.Fatal(err)
	}
	if err := a.backfillHistoricalApprovalEvents(); err != nil {
		log.Fatal(err)
	}
	a.ssh = newSSHManager(a)
	a.runtimes = newRuntimeRegistry(a)
	a.ssh.restorePersistedConnections(context.Background())

	if err := a.applyRemoteControlSettings(a.currentSettings().RemoteControl); err != nil {
		log.Fatal(err)
	}
	if err := a.applyCloudSettings(a.currentSettings().Cloud); err != nil {
		log.Fatal(err)
	}
	a.network.start(context.Background())

	if err := a.writeRuntimeFile(); err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/health", a.handleHealth)
	mux.HandleFunc("/v1/control/ws", a.handleControlWS)
	mux.HandleFunc("/v1/host", a.auth(a.handleHost))
	mux.HandleFunc("/v1/snapshot", a.auth(a.handleHostSnapshot))
	mux.HandleFunc("/v1/workbench", a.auth(a.handleWorkbench))
	mux.HandleFunc("/v1/settings", a.auth(a.handleSettings))
	mux.HandleFunc("/v1/settings/", a.auth(a.handleSettingsAction))
	mux.HandleFunc("/v1/cloud/auth/callback", a.handleCloudAuthCallback)
	mux.HandleFunc("/v1/cloud/auth/", a.auth(a.handleCloudAuthAction))
	mux.HandleFunc("/v1/cloud/account", a.auth(a.handleCloudAccount))
	mux.HandleFunc("/v1/cloud/account/relay", a.auth(a.handleCloudAccountRelay))
	mux.HandleFunc("/v1/cloud/relays", a.auth(a.handleCloudRelays))
	mux.HandleFunc("/v1/cloud/devices", a.auth(a.handleCloudDevices))
	mux.HandleFunc("/v1/cloud/devices/", a.auth(a.handleCloudDeviceAction))
	mux.HandleFunc("/v1/cloud/heartbeat", a.auth(a.handleCloudHeartbeat))
	mux.HandleFunc("/v1/cloud/pairing/requests", a.auth(a.handleCloudPairingRequests))
	mux.HandleFunc("/v1/cloud/pairing/requests/", a.auth(a.handleCloudPairingRequestAction))
	mux.HandleFunc("/v1/pairing/requests", a.auth(a.handlePairingRequests))
	mux.HandleFunc("/v1/pairing/requests/", a.auth(a.handlePairingRequestAction))
	mux.HandleFunc("/v1/trust/devices", a.auth(a.handleTrustDevices))
	mux.HandleFunc("/v1/trust/devices/", a.auth(a.handleTrustDeviceAction))
	mux.HandleFunc("/v1/mesh/state", a.auth(a.handleMeshState))
	mux.HandleFunc("/v1/remote/hosts", a.auth(a.handleRemoteHosts))
	mux.HandleFunc("/v1/remote/hosts/", a.auth(a.handleRemoteHostAction))
	mux.HandleFunc("/v1/fs/browse", a.auth(a.handleHostFileSystemBrowse))
	mux.HandleFunc("/v1/workspaces", a.auth(a.handleWorkspaces))
	mux.HandleFunc("/v1/workspaces/", a.auth(a.handleWorkspaceAction))
	mux.HandleFunc("/v1/codex-exec/", a.auth(a.handleCodexExecServerWS))
	mux.HandleFunc("/v1/sessions", a.auth(a.handleSessions))
	mux.HandleFunc("/v1/sessions/", a.auth(a.handleSessionAction))
	mux.HandleFunc("/v1/approvals/", a.auth(a.handleApprovalAction))
	mux.HandleFunc("/v1/events", a.auth(a.handleEvents))

	handler := withCORS(a.diagnosticHTTPLogger(mux))
	log.Printf("astralopsd listening on 127.0.0.1:%d", port)
	if err := http.Serve(ln, handler); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET, PATCH, POST, DELETE, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func defaultDataDir() string {
	if v := os.Getenv("ASTRALOPS_HOME"); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".AstralOps")
}

func randomToken() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

func writeRuntime(dataDir string, port int, token string, remoteControlAddr string) error {
	path := filepath.Join(dataDir, "runtime", "daemon.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	value := map[string]any{
		"host":       "127.0.0.1",
		"port":       port,
		"token":      token,
		"pid":        os.Getpid(),
		"updated_at": time.Now().UTC().Format(time.RFC3339Nano),
	}
	if remoteControlAddr != "" {
		value["remote_control"] = map[string]any{
			"listen_addr": remoteControlAddr,
			"paths":       []string{"/v1/host", "/v1/control/ws"},
		}
	}
	body, _ := json.MarshalIndent(value, "", "  ")
	return os.WriteFile(path, body, 0o600)
}

func (a *app) writeRuntimeFile() error {
	if a == nil || a.store == nil {
		return errors.New("store is not initialized")
	}
	return writeRuntime(a.store.dataDir, a.runtimePort, a.token, a.remoteControlListenAddr())
}

func (a *app) codexExecServerURL(workspaceID string) string {
	return fmt.Sprintf("ws://%s/v1/codex-exec/%s?token=%s", a.addr, workspaceID, a.token)
}

func randomID(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	return hex.EncodeToString(buf)[:n]
}

func randomUUID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	buf[6] = (buf[6] & 0x0f) | 0x40
	buf[8] = (buf[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", buf[0:4], buf[4:6], buf[6:8], buf[8:10], buf[10:16])
}

func discoverAgents() map[AgentKind]agentInfo {
	claude := discoverAgent("claude", "--version")
	enrichClaudeAgent(&claude)
	codex := discoverAgent("codex", "--version")
	enrichCodexAgent(&codex)
	return map[AgentKind]agentInfo{
		AgentClaude: claude,
		AgentCodex:  codex,
	}
}

func discoverAgent(name string, args ...string) agentInfo {
	path, err := exec.LookPath(name)
	if err != nil {
		for _, dir := range []string{"/opt/homebrew/bin", "/usr/local/bin", "/usr/bin", "/bin"} {
			candidate := filepath.Join(dir, name)
			if st, statErr := os.Stat(candidate); statErr == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
				path = candidate
				err = nil
				break
			}
		}
		if err != nil {
			return agentInfo{Available: false}
		}
	}
	info := agentInfo{Path: path, Available: true}
	ctx := exec.Command(path, args...)
	out, err := ctx.CombinedOutput()
	if err == nil {
		info.Version = strings.TrimSpace(string(out))
	}
	return info
}

func enrichClaudeAgent(info *agentInfo) {
	claudeEfforts := []string{"low", "medium", "high"}
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	settings := mergeJSONSettings(
		filepath.Join(home, ".claude", "settings.json"),
		filepath.Join(home, ".claude", "settings.local.json"),
	)
	if len(settings) == 0 {
		return
	}

	if model := stringSetting(settings, "model"); model != "" {
		info.CurrentModel = model
	}
	if effort := stringSetting(settings, "effortLevel"); effort != "" {
		info.CurrentEffort = effort
	}
	env := map[string]any{}
	if envSettings, ok := settings["env"].(map[string]any); ok {
		env = envSettings
		if info.CurrentModel == "" {
			info.CurrentModel = stringSetting(env, "ANTHROPIC_MODEL")
		}
	}
	info.Models = []modelInfo{
		claudeModelSlot("opus", stringSetting(env, "ANTHROPIC_DEFAULT_OPUS_MODEL"), claudeEfforts),
		claudeModelSlot("sonnet", stringSetting(env, "ANTHROPIC_DEFAULT_SONNET_MODEL"), claudeEfforts),
		claudeModelSlot("haiku", stringSetting(env, "ANTHROPIC_DEFAULT_HAIKU_MODEL"), claudeEfforts),
	}
}

func claudeModelSlot(alias, mapped string, supportedEfforts []string) modelInfo {
	if mapped == "" {
		return modelInfo{
			ID:                        alias,
			Label:                     titleCase(alias),
			Source:                    "Claude alias",
			Slot:                      alias,
			SupportedReasoningEfforts: supportedEfforts,
		}
	}
	return modelInfo{
		ID:                        mapped,
		Label:                     mapped,
		Source:                    "ANTHROPIC_DEFAULT_" + strings.ToUpper(alias) + "_MODEL",
		Slot:                      alias,
		SupportedReasoningEfforts: supportedEfforts,
	}
}

func enrichCodexAgent(info *agentInfo) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	add := newModelCollector()

	configPath := filepath.Join(home, ".codex", "config.toml")
	config := readSimpleTOML(configPath)
	if model := config["model"]; model != "" {
		info.CurrentModel = model
		add.add(model, "Codex config", info.CurrentEffort, nil)
	}
	if effort := config["model_reasoning_effort"]; effort != "" {
		info.CurrentEffort = effort
	}

	cachePath := filepath.Join(home, ".codex", "models_cache.json")
	var cache struct {
		Models []struct {
			Slug                          string `json:"slug"`
			DisplayName                   string `json:"display_name"`
			DefaultReasoningLevel         string `json:"default_reasoning_level"`
			ContextWindow                 int    `json:"context_window"`
			MaxContextWindow              int    `json:"max_context_window"`
			EffectiveContextWindowPercent int    `json:"effective_context_window_percent"`
			SupportedReasoningLevels      []struct {
				Effort string `json:"effort"`
			} `json:"supported_reasoning_levels"`
		} `json:"models"`
	}
	if body, err := os.ReadFile(cachePath); err == nil && json.Unmarshal(body, &cache) == nil {
		for _, model := range cache.Models {
			id := strings.TrimSpace(model.Slug)
			if id == "" {
				continue
			}
			label := strings.TrimSpace(model.DisplayName)
			if label == "" {
				label = id
			}
			efforts := []string{}
			for _, level := range model.SupportedReasoningLevels {
				if effort := strings.TrimSpace(level.Effort); effort != "" {
					efforts = append(efforts, effort)
				}
			}
			info := modelInfo{
				ID:                            id,
				Label:                         label,
				Source:                        "Codex models",
				DefaultReasoningEffort:        model.DefaultReasoningLevel,
				SupportedReasoningEfforts:     efforts,
				ContextWindow:                 model.ContextWindow,
				MaxContextWindow:              model.MaxContextWindow,
				EffectiveContextWindowPercent: model.EffectiveContextWindowPercent,
				EffectiveContextWindow:        effectiveContextWindow(model.ContextWindow, model.EffectiveContextWindowPercent),
			}
			add.addModel(info)
		}
	}
	if info.CurrentModel != "" {
		add.add(info.CurrentModel, "current", info.CurrentEffort, nil)
	}
	info.Models = add.models()
}

func mergeJSONSettings(paths ...string) map[string]any {
	out := map[string]any{}
	for _, path := range paths {
		body, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var next map[string]any
		if json.Unmarshal(body, &next) != nil {
			continue
		}
		mergeMap(out, next)
	}
	return out
}

func mergeMap(dst, src map[string]any) {
	for key, value := range src {
		srcMap, srcIsMap := value.(map[string]any)
		dstMap, dstIsMap := dst[key].(map[string]any)
		if srcIsMap && dstIsMap {
			mergeMap(dstMap, srcMap)
			continue
		}
		dst[key] = value
	}
}

func stringSetting(settings map[string]any, key string) string {
	value, ok := settings[key].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}

func titleCase(value string) string {
	if value == "" {
		return ""
	}
	return strings.ToUpper(value[:1]) + value[1:]
}

func readSimpleTOML(path string) map[string]string {
	out := map[string]string{}
	body, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "[") {
			continue
		}
		key, raw, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value := strings.TrimSpace(raw)
		if cut, _, ok := strings.Cut(value, "#"); ok {
			value = strings.TrimSpace(cut)
		}
		value = strings.Trim(value, `"'`)
		if key != "" && value != "" {
			out[key] = value
		}
	}
	return out
}

type modelCollector struct {
	order []string
	seen  map[string]modelInfo
}

func newModelCollector() *modelCollector {
	return &modelCollector{seen: map[string]modelInfo{}}
}

func (c *modelCollector) withLabel(id, label, source, defaultEffort string, supportedEfforts []string) {
	c.addModel(modelInfo{ID: id, Label: label, Source: source, DefaultReasoningEffort: defaultEffort, SupportedReasoningEfforts: supportedEfforts})
}

func (c *modelCollector) addModel(next modelInfo) {
	id := strings.TrimSpace(next.ID)
	if id == "" {
		return
	}
	if _, ok := c.seen[id]; !ok {
		c.order = append(c.order, id)
	}
	existing := c.seen[id]
	if next.Label == "" {
		next.Label = existing.Label
	}
	if next.Source == "" {
		next.Source = existing.Source
	}
	if next.DefaultReasoningEffort == "" {
		next.DefaultReasoningEffort = existing.DefaultReasoningEffort
	}
	if len(next.SupportedReasoningEfforts) == 0 {
		next.SupportedReasoningEfforts = existing.SupportedReasoningEfforts
	}
	if next.Slot == "" {
		next.Slot = existing.Slot
	}
	if next.ContextWindow == 0 {
		next.ContextWindow = existing.ContextWindow
	}
	if next.MaxContextWindow == 0 {
		next.MaxContextWindow = existing.MaxContextWindow
	}
	if next.EffectiveContextWindowPercent == 0 {
		next.EffectiveContextWindowPercent = existing.EffectiveContextWindowPercent
	}
	if next.EffectiveContextWindow == 0 {
		next.EffectiveContextWindow = existing.EffectiveContextWindow
	}
	c.seen[id] = modelInfo{
		ID:                            id,
		Label:                         next.Label,
		Source:                        next.Source,
		Slot:                          next.Slot,
		DefaultReasoningEffort:        next.DefaultReasoningEffort,
		SupportedReasoningEfforts:     dedupeStrings(next.SupportedReasoningEfforts),
		ContextWindow:                 next.ContextWindow,
		MaxContextWindow:              next.MaxContextWindow,
		EffectiveContextWindow:        next.EffectiveContextWindow,
		EffectiveContextWindowPercent: next.EffectiveContextWindowPercent,
	}
}

func (c *modelCollector) models() []modelInfo {
	out := make([]modelInfo, 0, len(c.order))
	for _, id := range c.order {
		out = append(out, c.seen[id])
	}
	return out
}

func (c *modelCollector) add(id, source, defaultEffort string, supportedEfforts []string) {
	c.withLabel(id, id, source, defaultEffort, supportedEfforts)
}

func effectiveContextWindow(contextWindow int, percent int) int {
	if contextWindow <= 0 {
		return 0
	}
	if percent <= 0 {
		return contextWindow
	}
	return int(float64(contextWindow) * float64(percent) / 100)
}

func dedupeStrings(values []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}
