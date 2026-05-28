package main

import (
	"bytes"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

func runControlDevClient(args []string) bool {
	if len(args) == 0 || args[0] != "control-client" {
		return false
	}
	if err := runControlDevClientCommand(args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	return true
}

func runControlDevClientCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: control-client <identity|known-hosts|discover|pair|workspaces|sessions|session-view|events|trust-list|request|smoke>")
	}
	st, err := loadStore(defaultDataDir())
	if err != nil {
		return err
	}
	switch args[0] {
	case "identity":
		return writePrettyJSON(os.Stdout, st.hostInfo().Identity)
	case "known-hosts":
		return writePrettyJSON(os.Stdout, st.listKnownHosts())
	case "discover":
		fs := flag.NewFlagSet("control-client discover", flag.ContinueOnError)
		timeout := fs.Duration("timeout", 3*time.Second, "LAN discovery timeout")
		port := fs.Int("port", defaultRemoteControlDiscoveryPort, "LAN discovery UDP port")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		candidates, err := discoverRemoteControlHostsWithTimeout(*timeout, *port)
		if err != nil {
			return err
		}
		return writePrettyJSON(os.Stdout, candidates)
	case "pair":
		fs := flag.NewFlagSet("control-client pair", flag.ContinueOnError)
		host := fs.String("host", "", "remote Host base URL, for example http://10.0.0.10:43900")
		capabilityList := fs.String("capabilities", strings.Join(defaultHostCapabilities(), ","), "comma-separated capabilities")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *host == "" {
			return fmt.Errorf("--host is required")
		}
		grant, err := controlClientPair(*host, st, parseCapabilityList(*capabilityList))
		if err != nil {
			return err
		}
		return writePrettyJSON(os.Stdout, grant)
	case "workspaces":
		fs := flag.NewFlagSet("control-client workspaces", flag.ContinueOnError)
		host := fs.String("host", "", "remote Host base URL")
		discover := fs.Bool("discover", false, "discover a known Host on LAN before connecting")
		hostDeviceID := fs.String("host-device-id", "", "known Host device id for LAN discovery")
		discoveryPort := fs.Int("discovery-port", defaultRemoteControlDiscoveryPort, "LAN discovery UDP port")
		discoveryTimeout := fs.Duration("discovery-timeout", 3*time.Second, "LAN discovery timeout")
		lanTimeout := fs.Duration("lan-timeout", 2*time.Second, "LAN host validation and handshake timeout")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		target, err := controlClientResolveTarget(st, controlClientTargetOptions{
			Host:             *host,
			Discover:         *discover,
			HostDeviceID:     *hostDeviceID,
			DiscoveryPort:    *discoveryPort,
			DiscoveryTimeout: *discoveryTimeout,
			LANTimeout:       *lanTimeout,
		})
		if err != nil {
			return err
		}
		response, err := controlClientRequestToTarget(target, st, ControlRequest{
			RequestID:  "dev_workspaces",
			Capability: CapabilityCoreRead,
			Action:     ControlActionWorkspaces,
		})
		if err != nil {
			return err
		}
		return writePrettyJSON(os.Stdout, response)
	case "sessions":
		fs := flag.NewFlagSet("control-client sessions", flag.ContinueOnError)
		host := fs.String("host", "", "remote Host base URL")
		discover := fs.Bool("discover", false, "discover a known Host on LAN before connecting")
		hostDeviceID := fs.String("host-device-id", "", "known Host device id for LAN discovery")
		discoveryPort := fs.Int("discovery-port", defaultRemoteControlDiscoveryPort, "LAN discovery UDP port")
		discoveryTimeout := fs.Duration("discovery-timeout", 3*time.Second, "LAN discovery timeout")
		lanTimeout := fs.Duration("lan-timeout", 2*time.Second, "LAN host validation and handshake timeout")
		workspaceID := fs.String("workspace-id", "", "optional workspace id filter")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		target, err := controlClientResolveTarget(st, controlClientTargetOptions{
			Host:             *host,
			Discover:         *discover,
			HostDeviceID:     *hostDeviceID,
			DiscoveryPort:    *discoveryPort,
			DiscoveryTimeout: *discoveryTimeout,
			LANTimeout:       *lanTimeout,
		})
		if err != nil {
			return err
		}
		response, err := controlClientRequestToTarget(target, st, ControlRequest{
			RequestID:  "dev_sessions",
			Capability: CapabilityCoreRead,
			Action:     ControlActionSessions,
			Params:     map[string]any{"workspace_id": *workspaceID},
		})
		if err != nil {
			return err
		}
		return writePrettyJSON(os.Stdout, response)
	case "session-view":
		fs := flag.NewFlagSet("control-client session-view", flag.ContinueOnError)
		host := fs.String("host", "", "remote Host base URL")
		discover := fs.Bool("discover", false, "discover a known Host on LAN before connecting")
		hostDeviceID := fs.String("host-device-id", "", "known Host device id for LAN discovery")
		discoveryPort := fs.Int("discovery-port", defaultRemoteControlDiscoveryPort, "LAN discovery UDP port")
		discoveryTimeout := fs.Duration("discovery-timeout", 3*time.Second, "LAN discovery timeout")
		lanTimeout := fs.Duration("lan-timeout", 2*time.Second, "LAN host validation and handshake timeout")
		sessionID := fs.String("session-id", "", "session id to read")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if strings.TrimSpace(*sessionID) == "" {
			return fmt.Errorf("--session-id is required")
		}
		target, err := controlClientResolveTarget(st, controlClientTargetOptions{
			Host:             *host,
			Discover:         *discover,
			HostDeviceID:     *hostDeviceID,
			DiscoveryPort:    *discoveryPort,
			DiscoveryTimeout: *discoveryTimeout,
			LANTimeout:       *lanTimeout,
		})
		if err != nil {
			return err
		}
		response, err := controlClientRequestToTarget(target, st, ControlRequest{
			RequestID:  "dev_session_view",
			Capability: CapabilityCoreRead,
			Action:     ControlActionSessionView,
			Params:     map[string]any{"session_id": *sessionID},
		})
		if err != nil {
			return err
		}
		return writePrettyJSON(os.Stdout, response)
	case "events":
		fs := flag.NewFlagSet("control-client events", flag.ContinueOnError)
		host := fs.String("host", "", "remote Host base URL")
		discover := fs.Bool("discover", false, "discover a known Host on LAN before connecting")
		hostDeviceID := fs.String("host-device-id", "", "known Host device id for LAN discovery")
		discoveryPort := fs.Int("discovery-port", defaultRemoteControlDiscoveryPort, "LAN discovery UDP port")
		discoveryTimeout := fs.Duration("discovery-timeout", 3*time.Second, "LAN discovery timeout")
		lanTimeout := fs.Duration("lan-timeout", 2*time.Second, "LAN host validation and handshake timeout")
		workspaceID := fs.String("workspace-id", "", "optional workspace id filter")
		sessionID := fs.String("session-id", "", "optional session id filter")
		afterSeq := fs.Int64("after-seq", 0, "only return events after this seq")
		beforeSeq := fs.Int64("before-seq", 0, "only return events before this seq")
		limit := fs.Int("limit", 50, "maximum events to return; 0 returns all matching events")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		target, err := controlClientResolveTarget(st, controlClientTargetOptions{
			Host:             *host,
			Discover:         *discover,
			HostDeviceID:     *hostDeviceID,
			DiscoveryPort:    *discoveryPort,
			DiscoveryTimeout: *discoveryTimeout,
			LANTimeout:       *lanTimeout,
		})
		if err != nil {
			return err
		}
		response, err := controlClientRequestToTarget(target, st, ControlRequest{
			RequestID:  "dev_events",
			Capability: CapabilityCoreRead,
			Action:     ControlActionEvents,
			Params: map[string]any{
				"workspace_id": *workspaceID,
				"session_id":   *sessionID,
				"after_seq":    *afterSeq,
				"before_seq":   *beforeSeq,
				"limit":        *limit,
			},
		})
		if err != nil {
			return err
		}
		return writePrettyJSON(os.Stdout, response)
	case "trust-list":
		fs := flag.NewFlagSet("control-client trust-list", flag.ContinueOnError)
		host := fs.String("host", "", "remote Host base URL")
		discover := fs.Bool("discover", false, "discover a known Host on LAN before connecting")
		hostDeviceID := fs.String("host-device-id", "", "known Host device id for LAN discovery")
		discoveryPort := fs.Int("discovery-port", defaultRemoteControlDiscoveryPort, "LAN discovery UDP port")
		discoveryTimeout := fs.Duration("discovery-timeout", 3*time.Second, "LAN discovery timeout")
		lanTimeout := fs.Duration("lan-timeout", 2*time.Second, "LAN host validation and handshake timeout")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		target, err := controlClientResolveTarget(st, controlClientTargetOptions{
			Host:             *host,
			Discover:         *discover,
			HostDeviceID:     *hostDeviceID,
			DiscoveryPort:    *discoveryPort,
			DiscoveryTimeout: *discoveryTimeout,
			LANTimeout:       *lanTimeout,
		})
		if err != nil {
			return err
		}
		response, err := controlClientRequestToTarget(target, st, ControlRequest{
			RequestID:  "dev_trust_list",
			Capability: CapabilityHostManage,
			Action:     ControlActionHostTrustList,
		})
		if err != nil {
			return err
		}
		return writePrettyJSON(os.Stdout, response)
	case "request":
		fs := flag.NewFlagSet("control-client request", flag.ContinueOnError)
		host := fs.String("host", "", "remote Host base URL")
		discover := fs.Bool("discover", false, "discover a known Host on LAN before connecting")
		hostDeviceID := fs.String("host-device-id", "", "known Host device id for LAN discovery")
		discoveryPort := fs.Int("discovery-port", defaultRemoteControlDiscoveryPort, "LAN discovery UDP port")
		discoveryTimeout := fs.Duration("discovery-timeout", 3*time.Second, "LAN discovery timeout")
		lanTimeout := fs.Duration("lan-timeout", 2*time.Second, "LAN host validation and handshake timeout")
		action := fs.String("action", "", "control action")
		capability := fs.String("capability", "", "control capability")
		params := fs.String("params", "{}", "JSON params object")
		requestID := fs.String("request-id", "dev_request", "request id")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *action == "" || *capability == "" {
			return fmt.Errorf("--action and --capability are required")
		}
		var decodedParams map[string]any
		if err := json.Unmarshal([]byte(*params), &decodedParams); err != nil {
			return err
		}
		target, err := controlClientResolveTarget(st, controlClientTargetOptions{
			Host:             *host,
			Discover:         *discover,
			HostDeviceID:     *hostDeviceID,
			DiscoveryPort:    *discoveryPort,
			DiscoveryTimeout: *discoveryTimeout,
			LANTimeout:       *lanTimeout,
		})
		if err != nil {
			return err
		}
		response, err := controlClientRequestToTarget(target, st, ControlRequest{
			RequestID:  *requestID,
			Capability: *capability,
			Action:     *action,
			Params:     decodedParams,
		})
		if err != nil {
			return err
		}
		return writePrettyJSON(os.Stdout, response)
	case "smoke":
		fs := flag.NewFlagSet("control-client smoke", flag.ContinueOnError)
		host := fs.String("host", "", "remote Host base URL")
		discover := fs.Bool("discover", false, "discover a known Host on LAN before connecting")
		hostDeviceID := fs.String("host-device-id", "", "known Host device id for LAN discovery")
		discoveryPort := fs.Int("discovery-port", defaultRemoteControlDiscoveryPort, "LAN discovery UDP port")
		discoveryTimeout := fs.Duration("discovery-timeout", 3*time.Second, "LAN discovery timeout")
		lanTimeout := fs.Duration("lan-timeout", 2*time.Second, "LAN host validation and handshake timeout")
		workspaceID := fs.String("workspace-id", "", "workspace id for workspace/terminal checks")
		sessionID := fs.String("session-id", "", "session id for attachment/media checks")
		path := fs.String("path", ".", "workspace path to read when --workspace-id is set")
		streamPath := fs.String("stream-path", "", "optional workspace path to read via workspace.files.stream")
		streamChunkSize := fs.Int("stream-chunk-size", 64*1024, "workspace.files.stream chunk size")
		workspaceWriteSmoke := fs.Bool("workspace-write-smoke", false, "exercise workspace.files.write/apply_patch/move/delete in a temporary Host workspace path")
		sessions := fs.Bool("sessions", false, "verify core.read.sessions over the encrypted control channel")
		sessionView := fs.Bool("session-view", false, "verify core.read.session_view for --session-id over the encrypted control channel")
		events := fs.Bool("events", false, "verify core.read.events over the encrypted control channel")
		eventsLimit := fs.Int("events-limit", 10, "maximum events returned by --events smoke")
		eventSubscription := fs.Bool("event-subscription", false, "verify core.subscribe.events replay frames over the encrypted control channel")
		eventReplayLimit := fs.Int("event-replay-limit", 1, "maximum replayed events returned by --event-subscription smoke")
		attachmentPath := fs.String("attachment-path", "", "optional local Controller file to upload with chunked attachment.ingest")
		attachmentChunkSize := fs.Int("attachment-chunk-size", 64*1024, "attachment ingest chunk size")
		mediaEventSeq := fs.Int64("media-event-seq", 0, "optional transcript event seq for media.stream")
		mediaID := fs.String("media-id", "", "optional transcript media id for media.stream")
		mediaChunkSize := fs.Int("media-chunk-size", 64*1024, "media.stream chunk size")
		execCommand := fs.String("exec-command", "", "optional workspace.exec command to run")
		terminal := fs.Bool("terminal", false, "exercise Host-owned PTY open/attach/input/output/close")
		trustList := fs.Bool("trust-list", false, "verify host.trust.list over the encrypted control channel")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		result, err := runControlClientSmoke(st, controlClientSmokeOptions{
			Target: controlClientTargetOptions{
				Host:             *host,
				Discover:         *discover,
				HostDeviceID:     *hostDeviceID,
				DiscoveryPort:    *discoveryPort,
				DiscoveryTimeout: *discoveryTimeout,
				LANTimeout:       *lanTimeout,
			},
			WorkspaceID:         *workspaceID,
			SessionID:           *sessionID,
			Path:                *path,
			StreamPath:          *streamPath,
			StreamChunkSize:     *streamChunkSize,
			WorkspaceWriteSmoke: *workspaceWriteSmoke,
			Sessions:            *sessions,
			SessionView:         *sessionView,
			Events:              *events,
			EventsLimit:         *eventsLimit,
			EventSubscription:   *eventSubscription,
			EventReplayLimit:    *eventReplayLimit,
			AttachmentPath:      *attachmentPath,
			AttachmentChunkSize: *attachmentChunkSize,
			MediaEventSeq:       *mediaEventSeq,
			MediaID:             *mediaID,
			MediaChunkSize:      *mediaChunkSize,
			ExecCommand:         *execCommand,
			Terminal:            *terminal,
			TrustList:           *trustList,
		})
		if err != nil {
			return err
		}
		return writePrettyJSON(os.Stdout, result)
	default:
		return fmt.Errorf("unknown control-client command %q", args[0])
	}
}

type controlClientTargetOptions struct {
	Host             string
	Discover         bool
	HostDeviceID     string
	DiscoveryPort    int
	DiscoveryTimeout time.Duration
	LANTimeout       time.Duration
}

type controlClientTarget struct {
	BaseURL      string
	HostInfo     HostInfo
	Timeout      time.Duration
	FallbackHost string
}

type controlClientSmokeOptions struct {
	Target              controlClientTargetOptions
	WorkspaceID         string
	SessionID           string
	Path                string
	StreamPath          string
	StreamChunkSize     int
	WorkspaceWriteSmoke bool
	Sessions            bool
	SessionView         bool
	Events              bool
	EventsLimit         int
	EventSubscription   bool
	EventReplayLimit    int
	AttachmentPath      string
	AttachmentChunkSize int
	MediaEventSeq       int64
	MediaID             string
	MediaChunkSize      int
	ExecCommand         string
	Terminal            bool
	TrustList           bool
}

type controlClientSmokeResult struct {
	Target       string                   `json:"target"`
	HostDeviceID string                   `json:"host_device_id"`
	Steps        []controlClientSmokeStep `json:"steps"`
}

type controlClientSmokeStep struct {
	Name       string         `json:"name"`
	Capability string         `json:"capability"`
	Action     string         `json:"action"`
	OK         bool           `json:"ok"`
	Error      *ControlError  `json:"error,omitempty"`
	Summary    map[string]any `json:"summary,omitempty"`
}

func controlClientResolveTarget(st *store, opts controlClientTargetOptions) (controlClientTarget, error) {
	if opts.Host != "" && !opts.Discover {
		return controlClientExplicitTarget(opts.Host)
	}
	if !opts.Discover {
		return controlClientTarget{}, fmt.Errorf("--host is required unless --discover is set")
	}
	if opts.DiscoveryTimeout <= 0 {
		opts.DiscoveryTimeout = 3 * time.Second
	}
	if opts.LANTimeout <= 0 {
		opts.LANTimeout = 2 * time.Second
	}
	candidates, err := discoverRemoteControlHostsWithTimeout(opts.DiscoveryTimeout, opts.DiscoveryPort)
	if err != nil {
		return controlClientFallbackTarget(opts.Host, err)
	}
	candidate, knownHost, err := selectKnownLanCandidate(st, candidates, opts.HostDeviceID)
	if err != nil {
		return controlClientFallbackTarget(opts.Host, err)
	}
	client := &http.Client{Timeout: opts.LANTimeout}
	hostInfo, err := controlClientHostInfoWithClient(candidate.BaseURL, client)
	if err != nil {
		return controlClientFallbackTarget(opts.Host, err)
	}
	if err := validateKnownLanHost(candidate, knownHost, hostInfo); err != nil {
		return controlClientFallbackTarget(opts.Host, err)
	}
	return controlClientTarget{BaseURL: candidate.BaseURL, HostInfo: hostInfo, Timeout: opts.LANTimeout, FallbackHost: strings.TrimSpace(opts.Host)}, nil
}

func controlClientExplicitTarget(host string) (controlClientTarget, error) {
	hostInfo, err := controlClientHostInfo(host)
	if err != nil {
		return controlClientTarget{}, err
	}
	return controlClientTarget{BaseURL: host, HostInfo: hostInfo}, nil
}

func controlClientFallbackTarget(host string, lanErr error) (controlClientTarget, error) {
	if strings.TrimSpace(host) == "" {
		return controlClientTarget{}, lanErr
	}
	target, err := controlClientExplicitTarget(host)
	if err != nil {
		return controlClientTarget{}, fmt.Errorf("LAN target unavailable: %v; fallback host failed: %w", lanErr, err)
	}
	return target, nil
}

func runControlClientSmoke(st *store, opts controlClientSmokeOptions) (controlClientSmokeResult, error) {
	opts.WorkspaceID = strings.TrimSpace(opts.WorkspaceID)
	opts.SessionID = strings.TrimSpace(opts.SessionID)
	if opts.Path == "" {
		opts.Path = "."
	}
	if opts.WorkspaceID == "" && strings.TrimSpace(opts.Path) != "." {
		return controlClientSmokeResult{}, fmt.Errorf("--workspace-id is required for --path")
	}
	if opts.WorkspaceID == "" && strings.TrimSpace(opts.StreamPath) != "" {
		return controlClientSmokeResult{}, fmt.Errorf("--workspace-id is required for --stream-path")
	}
	if opts.WorkspaceID == "" && (strings.TrimSpace(opts.ExecCommand) != "" || opts.Terminal || opts.WorkspaceWriteSmoke) {
		return controlClientSmokeResult{}, fmt.Errorf("--workspace-id is required for --exec-command, --terminal, or --workspace-write-smoke")
	}
	if opts.SessionID == "" && strings.TrimSpace(opts.AttachmentPath) != "" {
		return controlClientSmokeResult{}, fmt.Errorf("--session-id is required for --attachment-path")
	}
	if opts.SessionView && opts.SessionID == "" {
		return controlClientSmokeResult{}, fmt.Errorf("--session-id is required for --session-view")
	}
	opts.MediaID = strings.TrimSpace(opts.MediaID)
	mediaRequested := opts.MediaEventSeq != 0 || opts.MediaID != ""
	if mediaRequested && opts.SessionID == "" {
		return controlClientSmokeResult{}, fmt.Errorf("--session-id is required for --media-event-seq and --media-id")
	}
	if mediaRequested && (opts.MediaEventSeq <= 0 || opts.MediaID == "") {
		return controlClientSmokeResult{}, fmt.Errorf("--media-event-seq and --media-id are required together")
	}
	target, err := controlClientResolveTarget(st, opts.Target)
	if err != nil {
		return controlClientSmokeResult{}, err
	}
	result := controlClientSmokeResult{
		Target:       target.BaseURL,
		HostDeviceID: target.HostInfo.Identity.DeviceID,
	}

	workspaces, err := controlClientSmokeRequest(st, target, "workspaces", CapabilityCoreRead, ControlActionWorkspaces, nil)
	result.Steps = append(result.Steps, controlClientSmokeStepFromResponse("workspaces", CapabilityCoreRead, ControlActionWorkspaces, workspaces, controlClientWorkspacesSmokeSummary(workspaces)))
	if err != nil {
		return result, err
	}
	if err := controlClientSmokeResponseError("workspaces", workspaces); err != nil {
		return result, err
	}

	if opts.WorkspaceID != "" {
		params := map[string]any{"workspace_id": opts.WorkspaceID, "path": opts.Path}
		files, err := controlClientSmokeRequest(st, target, "workspace_files_read", CapabilityWorkspaceFilesRead, ControlActionWorkspaceFilesRead, params)
		result.Steps = append(result.Steps, controlClientSmokeStepFromResponse("workspace_files_read", CapabilityWorkspaceFilesRead, ControlActionWorkspaceFilesRead, files, controlClientWorkspaceFilesSmokeSummary(files)))
		if err != nil {
			return result, err
		}
		if err := controlClientSmokeResponseError("workspace_files_read", files); err != nil {
			return result, err
		}
	}

	if opts.Sessions {
		sessions, err := controlClientSmokeRequest(st, target, "sessions", CapabilityCoreRead, ControlActionSessions, map[string]any{"workspace_id": opts.WorkspaceID})
		result.Steps = append(result.Steps, controlClientSmokeStepFromResponse("sessions", CapabilityCoreRead, ControlActionSessions, sessions, controlClientSessionsSmokeSummary(sessions)))
		if err != nil {
			return result, err
		}
		if err := controlClientSmokeResponseError("sessions", sessions); err != nil {
			return result, err
		}
	}

	if opts.SessionView {
		view, err := controlClientSmokeRequest(st, target, "session_view", CapabilityCoreRead, ControlActionSessionView, map[string]any{"session_id": opts.SessionID})
		result.Steps = append(result.Steps, controlClientSmokeStepFromResponse("session_view", CapabilityCoreRead, ControlActionSessionView, view, controlClientSessionViewSmokeSummary(view)))
		if err != nil {
			return result, err
		}
		if err := controlClientSmokeResponseError("session_view", view); err != nil {
			return result, err
		}
	}

	if opts.Events {
		params := map[string]any{
			"workspace_id": opts.WorkspaceID,
			"session_id":   opts.SessionID,
			"limit":        opts.EventsLimit,
		}
		events, err := controlClientSmokeRequest(st, target, "events", CapabilityCoreRead, ControlActionEvents, params)
		result.Steps = append(result.Steps, controlClientSmokeStepFromResponse("events", CapabilityCoreRead, ControlActionEvents, events, controlClientEventsSmokeSummary(events)))
		if err != nil {
			return result, err
		}
		if err := controlClientSmokeResponseError("events", events); err != nil {
			return result, err
		}
	}

	if opts.EventSubscription {
		step, err := controlClientSmokeEventSubscription(st, target, opts.WorkspaceID, opts.SessionID, opts.EventReplayLimit)
		result.Steps = append(result.Steps, step)
		if err != nil {
			return result, err
		}
	}

	if opts.SessionID != "" && strings.TrimSpace(opts.AttachmentPath) != "" {
		step, err := controlClientSmokeAttachmentIngest(st, target, opts.SessionID, opts.AttachmentPath, opts.AttachmentChunkSize)
		result.Steps = append(result.Steps, step)
		if err != nil {
			return result, err
		}
	}

	if opts.WorkspaceID != "" && strings.TrimSpace(opts.StreamPath) != "" {
		step, err := controlClientSmokeWorkspaceFileStream(st, target, opts.WorkspaceID, opts.StreamPath, opts.StreamChunkSize)
		result.Steps = append(result.Steps, step)
		if err != nil {
			return result, err
		}
	}

	if opts.WorkspaceID != "" && opts.WorkspaceWriteSmoke {
		steps, err := controlClientSmokeWorkspaceWriteFlow(st, target, opts.WorkspaceID)
		result.Steps = append(result.Steps, steps...)
		if err != nil {
			return result, err
		}
	}

	if opts.SessionID != "" && opts.MediaEventSeq > 0 && opts.MediaID != "" {
		step, err := controlClientSmokeMediaStream(st, target, opts.SessionID, opts.MediaEventSeq, opts.MediaID, opts.MediaChunkSize)
		result.Steps = append(result.Steps, step)
		if err != nil {
			return result, err
		}
	}

	if opts.TrustList {
		trustList, err := controlClientSmokeRequest(st, target, "host_trust_list", CapabilityHostManage, ControlActionHostTrustList, nil)
		result.Steps = append(result.Steps, controlClientSmokeStepFromResponse("host_trust_list", CapabilityHostManage, ControlActionHostTrustList, trustList, controlClientHostTrustListSmokeSummary(trustList)))
		if err != nil {
			return result, err
		}
		if err := controlClientSmokeResponseError("host_trust_list", trustList); err != nil {
			return result, err
		}
	}

	if opts.WorkspaceID != "" && strings.TrimSpace(opts.ExecCommand) != "" {
		params := map[string]any{"workspace_id": opts.WorkspaceID, "command": opts.ExecCommand}
		exec, err := controlClientSmokeRequest(st, target, "workspace_exec", CapabilityWorkspaceExec, ControlActionWorkspaceExec, params)
		result.Steps = append(result.Steps, controlClientSmokeStepFromResponse("workspace_exec", CapabilityWorkspaceExec, ControlActionWorkspaceExec, exec, controlClientWorkspaceExecSmokeSummary(exec)))
		if err != nil {
			return result, err
		}
		if err := controlClientSmokeResponseError("workspace_exec", exec); err != nil {
			return result, err
		}
		if exitCode := int(numberValue(mapValue(exec.Result)["exit_code"])); exitCode != 0 {
			return result, fmt.Errorf("smoke step workspace_exec failed: exit_code=%d", exitCode)
		}
	}

	if opts.WorkspaceID != "" && opts.Terminal {
		steps, err := controlClientSmokeTerminalFlow(st, target, opts.WorkspaceID)
		result.Steps = append(result.Steps, steps...)
		if err != nil {
			return result, err
		}
	}
	return result, nil
}

func controlClientSmokeWorkspaceFileStream(st *store, target controlClientTarget, workspaceID, path string, chunkSize int) (controlClientSmokeStep, error) {
	name := "workspace_files_stream"
	params := map[string]any{"workspace_id": workspaceID, "path": path}
	if chunkSize > 0 {
		params["chunk_size"] = chunkSize
	}
	socket, cipher, activeTarget, err := controlClientDialTarget(target, st)
	if err != nil {
		step := controlClientSmokeStep{Name: name, Capability: CapabilityWorkspaceFilesRead, Action: ControlActionWorkspaceFilesStream, OK: false, Error: &ControlError{Code: "connect_failed", Message: err.Error()}}
		return step, err
	}
	defer socket.Close()

	req := ControlRequest{
		RequestID:  "smoke_" + name,
		Capability: CapabilityWorkspaceFilesRead,
		Action:     ControlActionWorkspaceFilesStream,
		Params:     params,
	}
	if err := controlClientWrite(socket, cipher, controlPlainFrame{Type: "request", Request: &req}); err != nil {
		step := controlClientSmokeStep{Name: name, Capability: CapabilityWorkspaceFilesRead, Action: ControlActionWorkspaceFilesStream, OK: false, Error: &ControlError{Code: "write_failed", Message: err.Error()}}
		return step, err
	}
	plain, err := controlClientReadWithTimeout(socket, cipher, activeTarget.Timeout)
	if err != nil {
		step := controlClientSmokeStep{Name: name, Capability: CapabilityWorkspaceFilesRead, Action: ControlActionWorkspaceFilesStream, OK: false, Error: &ControlError{Code: "read_failed", Message: err.Error()}}
		return step, err
	}
	if plain.Response == nil {
		err := fmt.Errorf("remote did not return a response frame")
		step := controlClientSmokeStep{Name: name, Capability: CapabilityWorkspaceFilesRead, Action: ControlActionWorkspaceFilesStream, OK: false, Error: &ControlError{Code: "invalid_response", Message: err.Error()}}
		return step, err
	}
	step := controlClientSmokeStepFromResponse(name, CapabilityWorkspaceFilesRead, ControlActionWorkspaceFilesStream, *plain.Response, controlClientWorkspaceFileStreamSmokeSummary(*plain.Response))
	if err := controlClientSmokeResponseError(name, *plain.Response); err != nil {
		return step, err
	}
	streamID := stringValue(mapValue(plain.Response.Result)["stream_id"])
	if streamID == "" {
		err := fmt.Errorf("smoke step %s failed: stream_id missing", name)
		step.OK = false
		step.Error = &ControlError{Code: "stream_id_missing", Message: err.Error()}
		return step, err
	}

	var bytesRead int64
	chunks := 0
	for {
		frame, err := controlClientReadWithTimeout(socket, cipher, activeTarget.Timeout)
		if err != nil {
			step.OK = false
			step.Error = &ControlError{Code: "stream_read_failed", Message: err.Error()}
			return step, err
		}
		if frame.WorkspaceFile == nil || frame.WorkspaceFile.StreamID != streamID {
			err := fmt.Errorf("unexpected workspace file stream frame")
			step.OK = false
			step.Error = &ControlError{Code: "unexpected_stream_frame", Message: err.Error()}
			return step, err
		}
		switch frame.Type {
		case workspaceFileStreamFrameChunk:
			body, err := base64.StdEncoding.DecodeString(frame.WorkspaceFile.DataBase64)
			if err != nil {
				step.OK = false
				step.Error = &ControlError{Code: "stream_chunk_invalid", Message: err.Error()}
				return step, err
			}
			chunks++
			bytesRead += int64(len(body))
		case workspaceFileStreamFrameComplete:
			if step.Summary == nil {
				step.Summary = map[string]any{}
			}
			step.Summary["chunks"] = chunks
			step.Summary["bytes"] = bytesRead
			step.Summary["final_offset"] = frame.WorkspaceFile.Offset
			return step, nil
		case workspaceFileStreamFrameError:
			err := fmt.Errorf("workspace file stream failed: %s", frame.WorkspaceFile.ErrorMessage)
			step.OK = false
			step.Error = &ControlError{Code: firstString(frame.WorkspaceFile.ErrorCode, "stream_error"), Message: err.Error()}
			return step, err
		default:
			err := fmt.Errorf("unexpected workspace file stream frame type %q", frame.Type)
			step.OK = false
			step.Error = &ControlError{Code: "unexpected_stream_frame", Message: err.Error()}
			return step, err
		}
	}
}

func controlClientSmokeMediaStream(st *store, target controlClientTarget, sessionID string, eventSeq int64, mediaID string, chunkSize int) (controlClientSmokeStep, error) {
	name := "media_stream"
	params := map[string]any{
		"session_id": sessionID,
		"event_seq":  eventSeq,
		"media_id":   mediaID,
	}
	if chunkSize > 0 {
		params["chunk_size"] = chunkSize
	}
	socket, cipher, activeTarget, err := controlClientDialTarget(target, st)
	if err != nil {
		step := controlClientSmokeStep{Name: name, Capability: CapabilityMediaStream, Action: ControlActionMediaStream, OK: false, Error: &ControlError{Code: "connect_failed", Message: err.Error()}}
		return step, err
	}
	defer socket.Close()

	req := ControlRequest{
		RequestID:  "smoke_" + name,
		Capability: CapabilityMediaStream,
		Action:     ControlActionMediaStream,
		Params:     params,
	}
	if err := controlClientWrite(socket, cipher, controlPlainFrame{Type: "request", Request: &req}); err != nil {
		step := controlClientSmokeStep{Name: name, Capability: CapabilityMediaStream, Action: ControlActionMediaStream, OK: false, Error: &ControlError{Code: "write_failed", Message: err.Error()}}
		return step, err
	}
	plain, err := controlClientReadWithTimeout(socket, cipher, activeTarget.Timeout)
	if err != nil {
		step := controlClientSmokeStep{Name: name, Capability: CapabilityMediaStream, Action: ControlActionMediaStream, OK: false, Error: &ControlError{Code: "read_failed", Message: err.Error()}}
		return step, err
	}
	if plain.Response == nil {
		err := fmt.Errorf("remote did not return a response frame")
		step := controlClientSmokeStep{Name: name, Capability: CapabilityMediaStream, Action: ControlActionMediaStream, OK: false, Error: &ControlError{Code: "invalid_response", Message: err.Error()}}
		return step, err
	}
	step := controlClientSmokeStepFromResponse(name, CapabilityMediaStream, ControlActionMediaStream, *plain.Response, controlClientMediaStreamSmokeSummary(*plain.Response))
	if err := controlClientSmokeResponseError(name, *plain.Response); err != nil {
		return step, err
	}
	streamID := stringValue(mapValue(plain.Response.Result)["stream_id"])
	if streamID == "" {
		err := fmt.Errorf("smoke step %s failed: stream_id missing", name)
		step.OK = false
		step.Error = &ControlError{Code: "stream_id_missing", Message: err.Error()}
		return step, err
	}

	var bytesRead int64
	chunks := 0
	for {
		frame, err := controlClientReadWithTimeout(socket, cipher, activeTarget.Timeout)
		if err != nil {
			step.OK = false
			step.Error = &ControlError{Code: "stream_read_failed", Message: err.Error()}
			return step, err
		}
		if frame.Media == nil || frame.Media.StreamID != streamID {
			err := fmt.Errorf("unexpected media stream frame")
			step.OK = false
			step.Error = &ControlError{Code: "unexpected_stream_frame", Message: err.Error()}
			return step, err
		}
		switch frame.Type {
		case mediaStreamFrameChunk:
			body, err := base64.StdEncoding.DecodeString(frame.Media.DataBase64)
			if err != nil {
				step.OK = false
				step.Error = &ControlError{Code: "stream_chunk_invalid", Message: err.Error()}
				return step, err
			}
			chunks++
			bytesRead += int64(len(body))
		case mediaStreamFrameComplete:
			if step.Summary == nil {
				step.Summary = map[string]any{}
			}
			step.Summary["chunks"] = chunks
			step.Summary["bytes"] = bytesRead
			step.Summary["final_offset"] = frame.Media.Offset
			return step, nil
		case mediaStreamFrameError:
			err := fmt.Errorf("media stream failed: %s", frame.Media.ErrorMessage)
			step.OK = false
			step.Error = &ControlError{Code: firstString(frame.Media.ErrorCode, "stream_error"), Message: err.Error()}
			return step, err
		default:
			err := fmt.Errorf("unexpected media stream frame type %q", frame.Type)
			step.OK = false
			step.Error = &ControlError{Code: "unexpected_stream_frame", Message: err.Error()}
			return step, err
		}
	}
}

func controlClientSmokeWorkspaceWriteFlow(st *store, target controlClientTarget, workspaceID string) ([]controlClientSmokeStep, error) {
	dir := ".astralops-control-smoke/" + randomID(8)
	writePath := dir + "/note.txt"
	movePath := dir + "/moved.txt"
	initialBody := []byte("astralops smoke before\n")

	write, err := controlClientSmokeRequest(st, target, "workspace_files_write", CapabilityWorkspaceFilesWrite, ControlActionWorkspaceFilesWrite, map[string]any{
		"workspace_id":   workspaceID,
		"path":           writePath,
		"content_base64": base64.StdEncoding.EncodeToString(initialBody),
		"create_parents": true,
	})
	steps := []controlClientSmokeStep{
		controlClientSmokeStepFromResponse("workspace_files_write", CapabilityWorkspaceFilesWrite, ControlActionWorkspaceFilesWrite, write, controlClientWorkspaceFilesWriteSmokeSummary(write)),
	}
	if err != nil {
		return steps, err
	}
	if err := controlClientSmokeResponseError("workspace_files_write", write); err != nil {
		return steps, err
	}

	patch, err := controlClientSmokeRequest(st, target, "workspace_files_apply_patch", CapabilityWorkspaceFilesWrite, ControlActionWorkspaceFilesApplyPatch, map[string]any{
		"workspace_id": workspaceID,
		"path":         writePath,
		"edits": []map[string]any{{
			"old_string": "before\n",
			"new_string": "after\n",
		}},
	})
	steps = append(steps, controlClientSmokeStepFromResponse("workspace_files_apply_patch", CapabilityWorkspaceFilesWrite, ControlActionWorkspaceFilesApplyPatch, patch, controlClientWorkspaceFilesApplyPatchSmokeSummary(patch)))
	if err != nil {
		return steps, err
	}
	if err := controlClientSmokeResponseError("workspace_files_apply_patch", patch); err != nil {
		return steps, err
	}

	move, err := controlClientSmokeRequest(st, target, "workspace_files_move", CapabilityWorkspaceFilesWrite, ControlActionWorkspaceFilesMove, map[string]any{
		"workspace_id":     workspaceID,
		"path":             writePath,
		"destination_path": movePath,
		"create_parents":   true,
	})
	steps = append(steps, controlClientSmokeStepFromResponse("workspace_files_move", CapabilityWorkspaceFilesWrite, ControlActionWorkspaceFilesMove, move, controlClientWorkspaceFilesMoveSmokeSummary(move)))
	if err != nil {
		return steps, err
	}
	if err := controlClientSmokeResponseError("workspace_files_move", move); err != nil {
		return steps, err
	}

	deleteResponse, err := controlClientSmokeRequest(st, target, "workspace_files_delete", CapabilityWorkspaceFilesWrite, ControlActionWorkspaceFilesDelete, map[string]any{
		"workspace_id": workspaceID,
		"path":         dir,
		"recursive":    true,
	})
	steps = append(steps, controlClientSmokeStepFromResponse("workspace_files_delete", CapabilityWorkspaceFilesWrite, ControlActionWorkspaceFilesDelete, deleteResponse, controlClientWorkspaceFilesDeleteSmokeSummary(deleteResponse)))
	if err != nil {
		return steps, err
	}
	if err := controlClientSmokeResponseError("workspace_files_delete", deleteResponse); err != nil {
		return steps, err
	}
	return steps, nil
}

func controlClientSmokeTerminalFlow(st *store, target controlClientTarget, workspaceID string) ([]controlClientSmokeStep, error) {
	socket, cipher, activeTarget, err := controlClientDialTarget(target, st)
	if err != nil {
		return []controlClientSmokeStep{{Name: "terminal_open", Capability: CapabilityTerminalOpen, Action: ControlActionTerminalOpen, Error: &ControlError{Code: "connect_failed", Message: err.Error()}}}, err
	}
	defer socket.Close()

	steps := []controlClientSmokeStep{}
	terminalID := ""
	closed := false
	defer func() {
		if terminalID != "" && !closed {
			_, _ = controlClientSmokeRequest(st, target, "terminal_close_cleanup", CapabilityTerminalInput, ControlActionTerminalClose, map[string]any{"terminal_id": terminalID})
		}
	}()

	open, err := controlClientRoundTrip(socket, cipher, activeTarget.Timeout, st, ControlRequest{
		RequestID:  "smoke_terminal_open",
		Capability: CapabilityTerminalOpen,
		Action:     ControlActionTerminalOpen,
		Params:     map[string]any{"workspace_id": workspaceID, "cols": 80, "rows": 24},
	})
	steps = append(steps, controlClientSmokeStepFromResponse("terminal_open", CapabilityTerminalOpen, ControlActionTerminalOpen, open, controlClientTerminalSmokeSummary(open)))
	if err != nil {
		return steps, err
	}
	if err := controlClientSmokeResponseError("terminal_open", open); err != nil {
		return steps, err
	}
	terminalID = stringValue(mapValue(open.Result)["terminal_id"])
	if terminalID == "" {
		err := fmt.Errorf("smoke step terminal_open failed: terminal_id missing")
		steps[len(steps)-1].OK = false
		steps[len(steps)-1].Error = &ControlError{Code: "terminal_id_missing", Message: err.Error()}
		return steps, err
	}

	attach, err := controlClientTerminalResponseRoundTrip(socket, cipher, activeTarget.Timeout, st, ControlRequest{
		RequestID:  "smoke_terminal_attach",
		Capability: CapabilityTerminalOpen,
		Action:     ControlActionTerminalAttach,
		Params:     map[string]any{"terminal_id": terminalID},
	})
	steps = append(steps, controlClientSmokeStepFromResponse("terminal_attach", CapabilityTerminalOpen, ControlActionTerminalAttach, attach, controlClientTerminalAttachSmokeSummary(attach)))
	if err != nil {
		return steps, err
	}
	if err := controlClientSmokeResponseError("terminal_attach", attach); err != nil {
		return steps, err
	}

	marker := "terminal-smoke-" + randomID(8)
	inputReq := ControlRequest{
		RequestID:  "smoke_terminal_input",
		Capability: CapabilityTerminalInput,
		Action:     ControlActionTerminalInput,
		Params: map[string]any{
			"terminal_id": terminalID,
			"data":        "printf '%s\\n' " + marker + "\n",
		},
	}
	inputReq.ControllerDeviceID = st.deviceIdentity.DeviceID
	if err := controlClientWrite(socket, cipher, controlPlainFrame{Type: "request", Request: &inputReq}); err != nil {
		step := controlClientSmokeStep{Name: "terminal_input", Capability: CapabilityTerminalInput, Action: ControlActionTerminalInput, Error: &ControlError{Code: "write_failed", Message: err.Error()}}
		steps = append(steps, step)
		return steps, err
	}
	var inputResponse *ControlResponse
	outputFrames := 0
	outputBytes := 0
	sawMarker := false
	for i := 0; i < 40 && (inputResponse == nil || !sawMarker); i++ {
		frame, err := controlClientReadWithTimeout(socket, cipher, activeTarget.Timeout)
		if err != nil {
			step := controlClientSmokeStep{Name: "terminal_output", Capability: CapabilityTerminalOpen, Action: terminalFrameOutput, Error: &ControlError{Code: "terminal_read_failed", Message: err.Error()}}
			steps = appendTerminalInputStep(steps, inputResponse)
			steps = append(steps, step)
			return steps, err
		}
		switch frame.Type {
		case "response":
			if frame.Response != nil && frame.Response.RequestID == inputReq.RequestID {
				response := *frame.Response
				inputResponse = &response
			}
		case terminalFrameOutput:
			if frame.Terminal == nil || frame.Terminal.TerminalID != terminalID {
				err := fmt.Errorf("unexpected terminal output frame")
				steps = appendTerminalInputStep(steps, inputResponse)
				steps = append(steps, controlClientSmokeStep{Name: "terminal_output", Capability: CapabilityTerminalOpen, Action: terminalFrameOutput, Error: &ControlError{Code: "unexpected_terminal_frame", Message: err.Error()}})
				return steps, err
			}
			outputFrames++
			outputBytes += len(frame.Terminal.Data)
			if strings.Contains(frame.Terminal.Data, marker) {
				sawMarker = true
			}
		case terminalFrameClosed:
			err := fmt.Errorf("terminal closed before smoke output was observed")
			steps = appendTerminalInputStep(steps, inputResponse)
			steps = append(steps, controlClientSmokeStep{Name: "terminal_output", Capability: CapabilityTerminalOpen, Action: terminalFrameOutput, Error: &ControlError{Code: "terminal_closed", Message: err.Error()}})
			return steps, err
		}
	}
	steps = appendTerminalInputStep(steps, inputResponse)
	if inputResponse == nil {
		err := fmt.Errorf("smoke step terminal_input failed: response missing")
		steps[len(steps)-1].Error = &ControlError{Code: "terminal_input_response_missing", Message: err.Error()}
		return steps, err
	}
	if err := controlClientSmokeResponseError("terminal_input", *inputResponse); err != nil {
		return steps, err
	}
	outputStep := controlClientSmokeStep{
		Name:       "terminal_output",
		Capability: CapabilityTerminalOpen,
		Action:     terminalFrameOutput,
		OK:         sawMarker,
		Summary: map[string]any{
			"terminal_id": terminalID,
			"frames":      outputFrames,
			"bytes":       outputBytes,
			"marker_seen": sawMarker,
		},
	}
	if !sawMarker {
		err := fmt.Errorf("smoke step terminal_output failed: marker output missing")
		outputStep.Error = &ControlError{Code: "terminal_output_missing", Message: err.Error()}
		steps = append(steps, outputStep)
		return steps, err
	}
	steps = append(steps, outputStep)

	closeSteps, err := controlClientSmokeTerminalClose(socket, cipher, activeTarget.Timeout, st, terminalID)
	steps = append(steps, closeSteps...)
	if err != nil {
		return steps, err
	}
	closed = true
	return steps, nil
}

func appendTerminalInputStep(steps []controlClientSmokeStep, response *ControlResponse) []controlClientSmokeStep {
	if response == nil {
		return append(steps, controlClientSmokeStep{Name: "terminal_input", Capability: CapabilityTerminalInput, Action: ControlActionTerminalInput})
	}
	return append(steps, controlClientSmokeStepFromResponse("terminal_input", CapabilityTerminalInput, ControlActionTerminalInput, *response, controlClientTerminalSmokeSummary(*response)))
}

func controlClientTerminalResponseRoundTrip(socket *websocket.Conn, cipher *controlCipher, timeout time.Duration, st *store, req ControlRequest) (ControlResponse, error) {
	req.ControllerDeviceID = st.deviceIdentity.DeviceID
	if err := controlClientWrite(socket, cipher, controlPlainFrame{Type: "request", Request: &req}); err != nil {
		return ControlResponse{}, err
	}
	for i := 0; i < 20; i++ {
		frame, err := controlClientReadWithTimeout(socket, cipher, timeout)
		if err != nil {
			return ControlResponse{}, err
		}
		if frame.Response != nil && frame.Response.RequestID == req.RequestID {
			return *frame.Response, nil
		}
		if frame.Type == terminalFrameClosed {
			return ControlResponse{}, fmt.Errorf("terminal closed before %s response", req.Action)
		}
	}
	return ControlResponse{}, fmt.Errorf("remote did not return response frame for %s", req.Action)
}

func controlClientSmokeTerminalClose(socket *websocket.Conn, cipher *controlCipher, timeout time.Duration, st *store, terminalID string) ([]controlClientSmokeStep, error) {
	req := ControlRequest{
		RequestID:  "smoke_terminal_close",
		Capability: CapabilityTerminalInput,
		Action:     ControlActionTerminalClose,
		Params:     map[string]any{"terminal_id": terminalID},
	}
	req.ControllerDeviceID = st.deviceIdentity.DeviceID
	if err := controlClientWrite(socket, cipher, controlPlainFrame{Type: "request", Request: &req}); err != nil {
		step := controlClientSmokeStep{Name: "terminal_close", Capability: CapabilityTerminalInput, Action: ControlActionTerminalClose, Error: &ControlError{Code: "write_failed", Message: err.Error()}}
		return []controlClientSmokeStep{step}, err
	}
	var closeResponse *ControlResponse
	sawClosedFrame := false
	for i := 0; i < 20 && (closeResponse == nil || !sawClosedFrame); i++ {
		frame, err := controlClientReadWithTimeout(socket, cipher, timeout)
		if err != nil {
			steps := appendTerminalCloseStep(nil, closeResponse, sawClosedFrame)
			steps = append(steps, controlClientSmokeStep{Name: "terminal_closed", Capability: CapabilityTerminalOpen, Action: terminalFrameClosed, Error: &ControlError{Code: "terminal_read_failed", Message: err.Error()}})
			return steps, err
		}
		switch frame.Type {
		case "response":
			if frame.Response != nil && frame.Response.RequestID == req.RequestID {
				response := *frame.Response
				closeResponse = &response
			}
		case terminalFrameClosed:
			if frame.Terminal != nil && frame.Terminal.TerminalID == terminalID && frame.Terminal.Status == terminalStatusClosed {
				sawClosedFrame = true
			}
		}
	}
	steps := appendTerminalCloseStep(nil, closeResponse, sawClosedFrame)
	if closeResponse == nil {
		err := fmt.Errorf("smoke step terminal_close failed: response missing")
		steps[0].Error = &ControlError{Code: "terminal_close_response_missing", Message: err.Error()}
		return steps, err
	}
	if err := controlClientSmokeResponseError("terminal_close", *closeResponse); err != nil {
		return steps, err
	}
	if !sawClosedFrame {
		err := fmt.Errorf("smoke step terminal_closed failed: closed frame missing")
		steps = append(steps, controlClientSmokeStep{Name: "terminal_closed", Capability: CapabilityTerminalOpen, Action: terminalFrameClosed, Error: &ControlError{Code: "terminal_closed_frame_missing", Message: err.Error()}})
		return steps, err
	}
	steps = append(steps, controlClientSmokeStep{
		Name:       "terminal_closed",
		Capability: CapabilityTerminalOpen,
		Action:     terminalFrameClosed,
		OK:         true,
		Summary:    map[string]any{"terminal_id": terminalID, "closed_frame": true},
	})
	return steps, nil
}

func appendTerminalCloseStep(steps []controlClientSmokeStep, response *ControlResponse, sawClosedFrame bool) []controlClientSmokeStep {
	if response == nil {
		return append(steps, controlClientSmokeStep{Name: "terminal_close", Capability: CapabilityTerminalInput, Action: ControlActionTerminalClose, Summary: map[string]any{"closed_frame": sawClosedFrame}})
	}
	step := controlClientSmokeStepFromResponse("terminal_close", CapabilityTerminalInput, ControlActionTerminalClose, *response, controlClientTerminalSmokeSummary(*response))
	if step.Summary == nil {
		step.Summary = map[string]any{}
	}
	step.Summary["closed_frame"] = sawClosedFrame
	return append(steps, step)
}

func controlClientSmokeRequest(st *store, target controlClientTarget, requestID, capability, action string, params map[string]any) (ControlResponse, error) {
	return controlClientRequestToTarget(target, st, ControlRequest{
		RequestID:  "smoke_" + requestID,
		Capability: capability,
		Action:     action,
		Params:     params,
	})
}

func controlClientSmokeEventSubscription(st *store, target controlClientTarget, workspaceID, sessionID string, replayLimit int) (controlClientSmokeStep, error) {
	name := "event_subscription"
	step := controlClientSmokeStep{Name: name, Capability: CapabilityCoreRead, Action: ControlActionEventsSubscribe}
	socket, cipher, activeTarget, err := controlClientDialTarget(target, st)
	if err != nil {
		step.Error = &ControlError{Code: "connect_failed", Message: err.Error()}
		return step, err
	}
	defer socket.Close()

	response, err := controlClientRoundTrip(socket, cipher, activeTarget.Timeout, st, ControlRequest{
		RequestID:  "smoke_event_subscription",
		Capability: CapabilityCoreRead,
		Action:     ControlActionEventsSubscribe,
		Params: map[string]any{
			"workspace_id": workspaceID,
			"session_id":   sessionID,
			"replay_limit": replayLimit,
		},
	})
	step = controlClientSmokeStepFromResponse(name, CapabilityCoreRead, ControlActionEventsSubscribe, response, controlClientEventSubscriptionSmokeSummary(response))
	if err != nil {
		step.Error = &ControlError{Code: "event_subscription_failed", Message: err.Error()}
		return step, err
	}
	if err := controlClientSmokeResponseError(name, response); err != nil {
		return step, err
	}
	streamID := stringValue(mapValue(response.Result)["stream_id"])
	if streamID == "" {
		err := fmt.Errorf("smoke step %s failed: stream_id missing", name)
		step.Error = &ControlError{Code: "event_subscription_stream_id_missing", Message: err.Error()}
		return step, err
	}

	plain, err := controlClientReadWithTimeout(socket, cipher, activeTarget.Timeout)
	if err != nil {
		step.Error = &ControlError{Code: "event_subscription_read_failed", Message: err.Error()}
		return step, err
	}
	if plain.Type != eventStreamFrameEvent || plain.Event == nil || plain.Event.StreamID != streamID {
		err := fmt.Errorf("unexpected event subscription frame")
		step.Error = &ControlError{Code: "unexpected_event_subscription_frame", Message: err.Error()}
		return step, err
	}
	if step.Summary == nil {
		step.Summary = map[string]any{}
	}
	step.Summary["event_seq"] = plain.Event.Seq
	step.Summary["event_kind"] = plain.Event.Event.Kind
	return step, nil
}

func controlClientSmokeAttachmentIngest(st *store, target controlClientTarget, sessionID, path string, chunkSize int) (controlClientSmokeStep, error) {
	name := "attachment_ingest"
	step := controlClientSmokeStep{Name: name, Capability: CapabilityAttachmentIngest, Action: "attachment.ingest.start/chunk/finish"}
	path = strings.TrimSpace(path)
	info, err := os.Stat(path)
	if err != nil {
		step.Error = &ControlError{Code: "attachment_file_not_found", Message: err.Error()}
		return step, err
	}
	if info.IsDir() {
		err := fmt.Errorf("attachment path is a directory")
		step.Error = &ControlError{Code: "attachment_path_is_directory", Message: err.Error()}
		return step, err
	}
	chunkSize = controlClientAttachmentChunkSize(chunkSize)
	sha, err := fileSHA256Hex(path)
	if err != nil {
		step.Error = &ControlError{Code: "attachment_hash_failed", Message: err.Error()}
		return step, err
	}

	socket, cipher, activeTarget, err := controlClientDialTarget(target, st)
	if err != nil {
		step.Error = &ControlError{Code: "connect_failed", Message: err.Error()}
		return step, err
	}
	defer socket.Close()

	start, err := controlClientRoundTrip(socket, cipher, activeTarget.Timeout, st, ControlRequest{
		RequestID:  "smoke_attachment_start",
		Capability: CapabilityAttachmentIngest,
		Action:     ControlActionAttachmentIngestStart,
		Params: map[string]any{
			"session_id": sessionID,
			"name":       filepath.Base(path),
			"mime_type":  attachmentMIMEType(filepath.Base(path), "", nil),
			"size":       info.Size(),
			"sha256":     sha,
		},
	})
	if err != nil {
		step.Error = &ControlError{Code: "attachment_start_failed", Message: err.Error()}
		return step, err
	}
	if err := controlClientSmokeResponseError("attachment_ingest.start", start); err != nil {
		step.Error = start.Error
		return step, err
	}
	uploadID := stringValue(mapValue(start.Result)["upload_id"])
	if uploadID == "" {
		err := fmt.Errorf("smoke step attachment_ingest.start failed: upload_id missing")
		step.Error = &ControlError{Code: "attachment_upload_id_missing", Message: err.Error()}
		return step, err
	}

	file, err := os.Open(path)
	if err != nil {
		step.Error = &ControlError{Code: "attachment_file_open_failed", Message: err.Error()}
		return step, err
	}
	defer file.Close()
	buffer := make([]byte, chunkSize)
	seq := int64(0)
	offset := int64(0)
	for {
		n, readErr := file.Read(buffer)
		if n > 0 {
			seq++
			chunk := buffer[:n]
			response, err := controlClientRoundTrip(socket, cipher, activeTarget.Timeout, st, ControlRequest{
				RequestID:  fmt.Sprintf("smoke_attachment_chunk_%d", seq),
				Capability: CapabilityAttachmentIngest,
				Action:     ControlActionAttachmentIngestChunk,
				Params: map[string]any{
					"session_id":  sessionID,
					"upload_id":   uploadID,
					"seq":         seq,
					"offset":      offset,
					"data_base64": base64.StdEncoding.EncodeToString(chunk),
				},
			})
			if err != nil {
				step.Error = &ControlError{Code: "attachment_chunk_failed", Message: err.Error()}
				return step, err
			}
			if err := controlClientSmokeResponseError("attachment_ingest.chunk", response); err != nil {
				step.Error = response.Error
				return step, err
			}
			offset += int64(n)
		}
		if readErr == nil {
			continue
		}
		if readErr == io.EOF {
			break
		}
		step.Error = &ControlError{Code: "attachment_file_read_failed", Message: readErr.Error()}
		return step, readErr
	}

	finish, err := controlClientRoundTrip(socket, cipher, activeTarget.Timeout, st, ControlRequest{
		RequestID:  "smoke_attachment_finish",
		Capability: CapabilityAttachmentIngest,
		Action:     ControlActionAttachmentIngestFinish,
		Params: map[string]any{
			"session_id": sessionID,
			"upload_id":  uploadID,
		},
	})
	if err != nil {
		step.Error = &ControlError{Code: "attachment_finish_failed", Message: err.Error()}
		return step, err
	}
	if err := controlClientSmokeResponseError("attachment_ingest.finish", finish); err != nil {
		step.Error = finish.Error
		return step, err
	}
	attachment := mapValue(mapValue(finish.Result)["attachment"])
	step.OK = true
	step.Summary = map[string]any{
		"session_id":    sessionID,
		"attachment_id": stringValue(attachment["id"]),
		"name":          stringValue(attachment["name"]),
		"kind":          stringValue(attachment["kind"]),
		"mime_type":     stringValue(attachment["mime_type"]),
		"host_owned":    boolValue(attachment["host_owned"]),
		"bytes":         offset,
		"chunks":        seq,
	}
	return step, nil
}

func controlClientAttachmentChunkSize(requested int) int {
	if requested <= 0 {
		return 64 * 1024
	}
	if requested > controlAttachmentChunkMaxBytes {
		return controlAttachmentChunkMaxBytes
	}
	return requested
}

func controlClientSmokeStepFromResponse(name, capability, action string, response ControlResponse, summary map[string]any) controlClientSmokeStep {
	step := controlClientSmokeStep{
		Name:       name,
		Capability: capability,
		Action:     action,
		OK:         response.OK,
		Error:      response.Error,
		Summary:    summary,
	}
	if len(step.Summary) == 0 {
		step.Summary = nil
	}
	return step
}

func controlClientSmokeResponseError(step string, response ControlResponse) error {
	if response.OK {
		return nil
	}
	if response.Error == nil {
		return fmt.Errorf("smoke step %s failed", step)
	}
	return fmt.Errorf("smoke step %s failed: %s", step, response.Error.Message)
}

func controlClientWorkspacesSmokeSummary(response ControlResponse) map[string]any {
	items, _ := response.Result.([]any)
	return map[string]any{"count": len(items)}
}

func controlClientSessionsSmokeSummary(response ControlResponse) map[string]any {
	items, _ := response.Result.([]any)
	return map[string]any{"count": len(items)}
}

func controlClientSessionViewSmokeSummary(response ControlResponse) map[string]any {
	result := mapValue(response.Result)
	session := mapValue(result["session"])
	events, _ := result["events"].([]any)
	return map[string]any{
		"session_id":   stringValue(session["id"]),
		"workspace_id": stringValue(session["workspace_id"]),
		"agent":        stringValue(session["agent"]),
		"event_count":  len(events),
	}
}

func controlClientEventsSmokeSummary(response ControlResponse) map[string]any {
	events, _ := response.Result.([]any)
	summary := map[string]any{"count": len(events)}
	if len(events) == 0 {
		return summary
	}
	first := mapValue(events[0])
	last := mapValue(events[len(events)-1])
	summary["first_seq"] = int64(numberValue(first["seq"]))
	summary["last_seq"] = int64(numberValue(last["seq"]))
	summary["last_kind"] = stringValue(last["kind"])
	return summary
}

func controlClientEventSubscriptionSmokeSummary(response ControlResponse) map[string]any {
	result := mapValue(response.Result)
	return map[string]any{
		"stream_id":    stringValue(result["stream_id"]),
		"workspace_id": stringValue(result["workspace_id"]),
		"session_id":   stringValue(result["session_id"]),
		"after_seq":    int64(numberValue(result["after_seq"])),
		"replay_limit": int(numberValue(result["replay_limit"])),
	}
}

func controlClientWorkspaceFilesSmokeSummary(response ControlResponse) map[string]any {
	result := mapValue(response.Result)
	return map[string]any{
		"workspace_id": stringValue(result["workspace_id"]),
		"path":         stringValue(result["path"]),
		"kind":         stringValue(result["kind"]),
		"target":       stringValue(result["target"]),
	}
}

func controlClientWorkspaceFilesWriteSmokeSummary(response ControlResponse) map[string]any {
	result := mapValue(response.Result)
	return map[string]any{
		"workspace_id": stringValue(result["workspace_id"]),
		"path":         stringValue(result["path"]),
		"kind":         stringValue(result["kind"]),
		"target":       stringValue(result["target"]),
		"size":         int64(numberValue(result["size"])),
	}
}

func controlClientWorkspaceFilesApplyPatchSmokeSummary(response ControlResponse) map[string]any {
	result := mapValue(response.Result)
	structuredPatch, _ := result["structured_patch"].([]any)
	return map[string]any{
		"workspace_id":           stringValue(result["workspace_id"]),
		"path":                   stringValue(result["path"]),
		"kind":                   stringValue(result["kind"]),
		"target":                 stringValue(result["target"]),
		"size":                   int64(numberValue(result["size"])),
		"applied_edits":          int(numberValue(result["applied_edits"])),
		"structured_patch_count": len(structuredPatch),
	}
}

func controlClientWorkspaceFilesMoveSmokeSummary(response ControlResponse) map[string]any {
	result := mapValue(response.Result)
	return map[string]any{
		"workspace_id": stringValue(result["workspace_id"]),
		"from_path":    stringValue(result["from_path"]),
		"to_path":      stringValue(result["to_path"]),
		"kind":         stringValue(result["kind"]),
		"target":       stringValue(result["target"]),
		"size":         int64(numberValue(result["size"])),
	}
}

func controlClientWorkspaceFilesDeleteSmokeSummary(response ControlResponse) map[string]any {
	result := mapValue(response.Result)
	return map[string]any{
		"workspace_id": stringValue(result["workspace_id"]),
		"path":         stringValue(result["path"]),
		"kind":         stringValue(result["kind"]),
		"target":       stringValue(result["target"]),
		"removed":      boolValue(result["removed"]),
	}
}

func controlClientWorkspaceExecSmokeSummary(response ControlResponse) map[string]any {
	result := mapValue(response.Result)
	return map[string]any{
		"workspace_id": stringValue(result["workspace_id"]),
		"exit_code":    int(numberValue(result["exit_code"])),
		"duration_ms":  int(numberValue(result["duration_ms"])),
	}
}

func controlClientWorkspaceFileStreamSmokeSummary(response ControlResponse) map[string]any {
	result := mapValue(response.Result)
	return map[string]any{
		"workspace_id": stringValue(result["workspace_id"]),
		"path":         stringValue(result["path"]),
		"kind":         stringValue(result["kind"]),
		"target":       stringValue(result["target"]),
		"size":         int64(numberValue(result["size"])),
		"offset":       int64(numberValue(result["offset"])),
		"chunk_size":   int(numberValue(result["chunk_size"])),
	}
}

func controlClientMediaStreamSmokeSummary(response ControlResponse) map[string]any {
	result := mapValue(response.Result)
	return map[string]any{
		"session_id":   stringValue(result["session_id"]),
		"event_seq":    int64(numberValue(result["event_seq"])),
		"media_id":     stringValue(result["media_id"]),
		"kind":         stringValue(result["kind"]),
		"name":         stringValue(result["name"]),
		"mime_type":    stringValue(result["mime_type"]),
		"size":         int64(numberValue(result["size"])),
		"offset":       int64(numberValue(result["offset"])),
		"chunk_size":   int(numberValue(result["chunk_size"])),
		"stream_id":    stringValue(result["stream_id"]),
		"resume_token": stringValue(result["resume_token"]),
	}
}

func controlClientHostTrustListSmokeSummary(response ControlResponse) map[string]any {
	result := mapValue(response.Result)
	grants, _ := result["grants"].([]any)
	return map[string]any{"count": len(grants)}
}

func controlClientTerminalSmokeSummary(response ControlResponse) map[string]any {
	result := mapValue(response.Result)
	return map[string]any{
		"terminal_id": stringValue(result["terminal_id"]),
		"status":      stringValue(result["status"]),
	}
}

func controlClientTerminalAttachSmokeSummary(response ControlResponse) map[string]any {
	result := mapValue(response.Result)
	return map[string]any{
		"terminal_id":      stringValue(result["terminal_id"]),
		"workspace_id":     stringValue(result["workspace_id"]),
		"target":           stringValue(result["target"]),
		"status":           stringValue(result["status"]),
		"viewer_device_id": stringValue(result["viewer_device_id"]),
		"connection_id":    stringValue(result["connection_id"]),
		"writer_device_id": stringValue(result["writer_device_id"]),
		"output_seq":       int64(numberValue(result["output_seq"])),
	}
}

func selectKnownLanCandidate(st *store, candidates []LanHostCandidate, hostDeviceID string) (LanHostCandidate, KnownHost, error) {
	hostDeviceID = strings.TrimSpace(hostDeviceID)
	matches := []struct {
		candidate LanHostCandidate
		knownHost KnownHost
	}{}
	for _, candidate := range candidates {
		if hostDeviceID != "" && candidate.DeviceID != hostDeviceID {
			continue
		}
		knownHost, ok := st.knownHost(candidate.DeviceID)
		if !ok {
			continue
		}
		if knownHost.PublicKeyFingerprint != candidate.PublicKeyFingerprint {
			continue
		}
		matches = append(matches, struct {
			candidate LanHostCandidate
			knownHost KnownHost
		}{candidate: candidate, knownHost: knownHost})
	}
	if len(matches) == 0 {
		if hostDeviceID == "" {
			return LanHostCandidate{}, KnownHost{}, fmt.Errorf("no known LAN Host candidates found; pair the Host first or pass --host")
		}
		return LanHostCandidate{}, KnownHost{}, fmt.Errorf("known Host %q was not found on LAN", hostDeviceID)
	}
	if len(matches) > 1 {
		return LanHostCandidate{}, KnownHost{}, fmt.Errorf("multiple known LAN Host candidates found; pass --host-device-id")
	}
	return matches[0].candidate, matches[0].knownHost, nil
}

func validateKnownLanHost(candidate LanHostCandidate, knownHost KnownHost, hostInfo HostInfo) error {
	if hostInfo.Identity.DeviceID != candidate.DeviceID {
		return fmt.Errorf("LAN candidate device_id mismatch: discovered %q but Host returned %q", candidate.DeviceID, hostInfo.Identity.DeviceID)
	}
	if hostInfo.Identity.DeviceID != knownHost.DeviceID {
		return fmt.Errorf("known Host device_id mismatch: known %q but Host returned %q", knownHost.DeviceID, hostInfo.Identity.DeviceID)
	}
	if hostInfo.Identity.PublicKey != knownHost.PublicKey {
		return fmt.Errorf("known Host public key mismatch for %s", knownHost.DeviceID)
	}
	if hostInfo.Identity.PublicKeyFingerprint != knownHost.PublicKeyFingerprint || candidate.PublicKeyFingerprint != knownHost.PublicKeyFingerprint {
		return fmt.Errorf("known Host public key fingerprint mismatch for %s", knownHost.DeviceID)
	}
	return nil
}

func controlClientPair(host string, st *store, capabilities []string) (TrustGrant, error) {
	hostInfo, err := controlClientHostInfo(host)
	if err != nil {
		return TrustGrant{}, err
	}
	identity := st.deviceIdentity
	req := trustDeviceRequest{
		ControllerDeviceID:             identity.DeviceID,
		ControllerDeviceName:           identity.DeviceName,
		ControllerPublicKey:            identity.PublicKey,
		ControllerPublicKeyFingerprint: identity.PublicKeyFingerprint,
		Capabilities:                   capabilities,
	}
	body, err := json.Marshal(req)
	if err != nil {
		return TrustGrant{}, err
	}
	httpResp, err := http.Post(controlHTTPURL(host, "/v1/trust/devices"), "application/json", bytes.NewReader(body))
	if err != nil {
		return TrustGrant{}, err
	}
	defer httpResp.Body.Close()
	responseBody, _ := io.ReadAll(httpResp.Body)
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return TrustGrant{}, fmt.Errorf("pairing failed: status %d: %s", httpResp.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	var grant TrustGrant
	if err := json.Unmarshal(responseBody, &grant); err != nil {
		return TrustGrant{}, err
	}
	if _, err := st.rememberKnownHost(hostInfo, host); err != nil {
		return TrustGrant{}, err
	}
	return grant, nil
}

func controlClientRequest(host string, st *store, req ControlRequest) (ControlResponse, error) {
	hostInfo, err := controlClientHostInfo(host)
	if err != nil {
		return ControlResponse{}, err
	}
	return controlClientRequestToTarget(controlClientTarget{BaseURL: host, HostInfo: hostInfo}, st, req)
}

func controlClientRequestToTarget(target controlClientTarget, st *store, req ControlRequest) (ControlResponse, error) {
	socket, cipher, activeTarget, err := controlClientDialTarget(target, st)
	if err != nil {
		return ControlResponse{}, err
	}
	defer socket.Close()

	return controlClientRoundTrip(socket, cipher, activeTarget.Timeout, st, req)
}

func controlClientRoundTrip(socket *websocket.Conn, cipher *controlCipher, timeout time.Duration, st *store, req ControlRequest) (ControlResponse, error) {
	req.ControllerDeviceID = st.deviceIdentity.DeviceID
	if err := controlClientWrite(socket, cipher, controlPlainFrame{Type: "request", Request: &req}); err != nil {
		return ControlResponse{}, err
	}
	plain, err := controlClientReadWithTimeout(socket, cipher, timeout)
	if err != nil {
		return ControlResponse{}, err
	}
	if plain.Response == nil {
		return ControlResponse{}, fmt.Errorf("remote did not return a response frame")
	}
	return *plain.Response, nil
}

func controlClientHostInfo(host string) (HostInfo, error) {
	return controlClientHostInfoWithClient(host, http.DefaultClient)
}

func controlClientHostInfoWithClient(host string, client *http.Client) (HostInfo, error) {
	resp, err := client.Get(controlHTTPURL(host, "/v1/host"))
	if err != nil {
		return HostInfo{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return HostInfo{}, fmt.Errorf("host info failed: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var info HostInfo
	if err := json.Unmarshal(body, &info); err != nil {
		return HostInfo{}, err
	}
	if info.Identity.DeviceID == "" || info.Identity.PublicKey == "" {
		return HostInfo{}, fmt.Errorf("remote Host did not return a usable identity")
	}
	return info, nil
}

func controlClientDial(host string, st *store, hostInfo HostInfo) (*websocket.Conn, *controlCipher, error) {
	return controlClientDialWithTimeout(host, st, hostInfo, 0)
}

func controlClientDialTarget(target controlClientTarget, st *store) (*websocket.Conn, *controlCipher, controlClientTarget, error) {
	socket, cipher, err := controlClientDialWithTimeout(target.BaseURL, st, target.HostInfo, target.Timeout)
	if err == nil {
		return socket, cipher, target, nil
	}
	if strings.TrimSpace(target.FallbackHost) == "" {
		return nil, nil, target, err
	}
	fallback, fallbackErr := controlClientExplicitTarget(target.FallbackHost)
	if fallbackErr != nil {
		return nil, nil, target, fmt.Errorf("LAN control channel failed: %v; fallback host failed: %w", err, fallbackErr)
	}
	if fallback.HostInfo.Identity.DeviceID != target.HostInfo.Identity.DeviceID || fallback.HostInfo.Identity.PublicKey != target.HostInfo.Identity.PublicKey {
		return nil, nil, target, fmt.Errorf("LAN control channel failed: %v; fallback host identity mismatch for %s", err, target.HostInfo.Identity.DeviceID)
	}
	socket, cipher, fallbackDialErr := controlClientDialWithTimeout(fallback.BaseURL, st, fallback.HostInfo, fallback.Timeout)
	if fallbackDialErr != nil {
		return nil, nil, target, fmt.Errorf("LAN control channel failed: %v; fallback control channel failed: %w", err, fallbackDialErr)
	}
	return socket, cipher, fallback, nil
}

func controlClientDialWithTimeout(host string, st *store, hostInfo HostInfo, timeout time.Duration) (*websocket.Conn, *controlCipher, error) {
	dialer := *websocket.DefaultDialer
	if timeout > 0 {
		dialer.HandshakeTimeout = timeout
	}
	socket, _, err := dialer.Dial(controlWSURL(host, "/v1/control/ws"), nil)
	if err != nil {
		return nil, nil, err
	}
	curve := ecdh.X25519()
	controllerEphemeral, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		socket.Close()
		return nil, nil, err
	}
	clientNonce, err := randomBase64(32)
	if err != nil {
		socket.Close()
		return nil, nil, err
	}
	hello := controlHelloFrame{
		Type:                   "hello",
		Version:                controlProtocolVersion,
		ControllerDeviceID:     st.deviceIdentity.DeviceID,
		ControllerPublicKey:    st.deviceIdentity.PublicKey,
		ControllerEphemeralKey: base64.StdEncoding.EncodeToString(controllerEphemeral.PublicKey().Bytes()),
		ClientNonce:            clientNonce,
	}
	hello.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(ed25519.PrivateKey(st.devicePrivateKey), controlClientSignaturePayload(hostInfo.Identity.DeviceID, hello)))
	if err := socket.WriteJSON(hello); err != nil {
		socket.Close()
		return nil, nil, err
	}
	var ack controlHelloAckFrame
	if err := socket.ReadJSON(&ack); err != nil {
		socket.Close()
		return nil, nil, err
	}
	if ack.Type != "hello_ack" || ack.Version != controlProtocolVersion {
		socket.Close()
		return nil, nil, fmt.Errorf("invalid control hello_ack")
	}
	if ack.HostDeviceID != hostInfo.Identity.DeviceID || ack.HostPublicKey != hostInfo.Identity.PublicKey {
		socket.Close()
		return nil, nil, fmt.Errorf("remote Host identity changed during handshake")
	}
	hostPublicKey, err := decodeDevicePublicKey(ack.HostPublicKey)
	if err != nil {
		socket.Close()
		return nil, nil, err
	}
	signature, err := base64.StdEncoding.DecodeString(ack.Signature)
	if err != nil || !ed25519.Verify(hostPublicKey, controlHostSignaturePayload(hello, ack), signature) {
		socket.Close()
		return nil, nil, fmt.Errorf("invalid Host hello_ack signature")
	}
	hostEphemeralBytes, err := base64.StdEncoding.DecodeString(ack.HostEphemeralKey)
	if err != nil {
		socket.Close()
		return nil, nil, err
	}
	hostEphemeral, err := curve.NewPublicKey(hostEphemeralBytes)
	if err != nil {
		socket.Close()
		return nil, nil, err
	}
	sharedSecret, err := controllerEphemeral.ECDH(hostEphemeral)
	if err != nil {
		socket.Close()
		return nil, nil, err
	}
	cipher, err := newControlCipher(deriveControlSessionKey(sharedSecret, hello, ack.HostDeviceID, ack.HostPublicKey, ack.HostEphemeralKey, ack.ServerNonce, ack.ConnectionID))
	if err != nil {
		socket.Close()
		return nil, nil, err
	}
	return socket, cipher, nil
}

func controlClientWrite(socket *websocket.Conn, cipher *controlCipher, frame controlPlainFrame) error {
	sealed, err := cipher.seal(frame)
	if err != nil {
		return err
	}
	return socket.WriteJSON(sealed)
}

func controlClientRead(socket *websocket.Conn, cipher *controlCipher) (controlPlainFrame, error) {
	var sealed controlSealedFrame
	if err := socket.ReadJSON(&sealed); err != nil {
		return controlPlainFrame{}, err
	}
	return cipher.open(sealed)
}

func controlClientReadWithTimeout(socket *websocket.Conn, cipher *controlCipher, timeout time.Duration) (controlPlainFrame, error) {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	_ = socket.SetReadDeadline(time.Now().Add(timeout))
	defer socket.SetReadDeadline(time.Time{})
	return controlClientRead(socket, cipher)
}

func controlHTTPURL(host, path string) string {
	host = strings.TrimRight(strings.TrimSpace(host), "/")
	if !strings.HasPrefix(host, "http://") && !strings.HasPrefix(host, "https://") {
		host = "http://" + host
	}
	return host + path
}

func controlWSURL(host, path string) string {
	url := controlHTTPURL(host, path)
	url = strings.TrimPrefix(url, "http://")
	if strings.HasPrefix(url, "https://") {
		return "wss://" + strings.TrimPrefix(url, "https://")
	}
	return "ws://" + url
}

func parseCapabilityList(value string) []string {
	items := strings.Split(value, ",")
	out := []string{}
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}

func writePrettyJSON(w io.Writer, value any) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
