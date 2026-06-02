package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/oines/astralops/pkg/controllercore"
	"github.com/oines/astralops/pkg/controlwire"
)

const remoteControlStreamBufferSize = 128

type daemonControllerTransport struct {
	deps controllerTransportDeps
}

type controllerTransportDeps struct {
	meshState        func(context.Context, bool) (meshStateResponse, error)
	requestPairing   func(context.Context, string) (controllercore.PairingSignal, error)
	managedTransport func() *controllercore.ManagedTransport
}

func (a *app) newControllerCore() *controllercore.Controller {
	return controllercore.New(daemonControllerTransport{deps: a.controllerTransportDeps()})
}

func (a *app) controllerTransportDeps() controllerTransportDeps {
	return controllerTransportDeps{
		meshState: func(ctx context.Context, discover bool) (meshStateResponse, error) {
			if a.mesh != nil {
				return a.mesh.refresh(ctx, discover)
			}
			return newMeshStateManager(meshStateDepsFromApp(a)).build(ctx, discover), nil
		},
		requestPairing:   a.requestControllerPairing,
		managedTransport: a.controllerManagedTransport,
	}
}

func (a *app) controllerManagedTransport() *controllercore.ManagedTransport {
	if a == nil {
		return nil
	}
	a.remoteControlMu.Lock()
	defer a.remoteControlMu.Unlock()
	if a.controllerTransport == nil {
		a.controllerTransport = a.newControllerManagedTransport()
	}
	return a.controllerTransport
}

func (a *app) newControllerManagedTransport() *controllercore.ManagedTransport {
	return controllercore.NewManagedTransport(controllercore.ManagedTransportConfig{
		OpenFrameConn: func(ctx context.Context, hostDeviceID string, preferRelay bool) (controllercore.FrameConn, controllercore.ResolvedTarget, error) {
			target, err := a.remoteHostTarget(hostDeviceID)
			if err != nil {
				return nil, controllercore.ResolvedTarget{HostDeviceID: hostDeviceID}, toCoreError(err)
			}
			if preferRelay && strings.TrimSpace(target.RelayClient.BaseURL) != "" && strings.TrimSpace(target.RelayClient.Token) != "" {
				target.UseRelay = true
			}
			conn, activeTarget, err := controlClientOpenTargetWithTransports(ctx, target, a.store, controlClientTransportPlan(target))
			resolved := toCoreResolvedTarget(hostDeviceID, activeTarget)
			if err != nil {
				return nil, resolved, toCoreError(err)
			}
			return daemonControllerFrameConn{conn: conn}, resolved, nil
		},
		SelfDeviceID: func() string {
			if a == nil || a.store == nil {
				return ""
			}
			return a.store.deviceIdentity.DeviceID
		},
		DecodeEvent: func(body json.RawMessage) (controllercore.EventEnvelope, bool) {
			var event AstralEvent
			if err := json.Unmarshal(body, &event); err != nil {
				return controllercore.EventEnvelope{}, false
			}
			return controllercore.EventEnvelope{Seq: event.Seq, Event: event}, true
		},
		StateChanged: func(hostDeviceID string, state controllercore.ControlState) {
			if a != nil {
				if core := a.controllerCoreManager(); core != nil {
					if session := core.OpenHostSession(hostDeviceID); session != nil {
						session.ApplyControlState(state)
					}
				}
				if manager := a.hostRemoteSessionManager(); manager != nil {
					manager.ApplyControlState(hostDeviceID, state)
				}
				a.refreshMeshStateAsync(true)
			}
		},
		Activity: func(hostDeviceID string) {
			if a == nil {
				return
			}
			if manager := a.hostRemoteSessionManager(); manager != nil {
				manager.MarkActivity(hostDeviceID)
			}
		},
		RefreshMesh: func(discover bool) {
			if a != nil {
				a.refreshMeshStateAsync(discover)
			}
		},
	})
}

func (a *app) controllerCoreManager() *controllercore.Controller {
	if a == nil {
		return nil
	}
	if !a.controllerRoleEnabled() {
		return nil
	}
	a.remoteControlMu.Lock()
	defer a.remoteControlMu.Unlock()
	if a.controllerCore == nil {
		a.controllerCore = a.newControllerCore()
	}
	return a.controllerCore
}

func (a *app) controllerCoreRequest(ctx context.Context, hostDeviceID, capability, action string, params map[string]any) (ControlResponse, error) {
	core := a.controllerCoreManager()
	if core == nil {
		return ControlResponse{}, errors.New("controller core is not initialized")
	}
	response, err := core.Request(ctx, hostDeviceID, capability, action, params)
	return fromCoreControlResponse(response), fromCoreError(err)
}

func (a *app) controllerCoreSubscribeEvents(ctx context.Context, hostDeviceID string, params eventSubscriptionParams) (remoteControlEventStream, error) {
	core := a.controllerCoreManager()
	if core == nil {
		return remoteControlEventStream{}, errors.New("controller core is not initialized")
	}
	session := core.OpenHostSession(hostDeviceID)
	if session == nil {
		return remoteControlEventStream{}, errors.New("remote Host device id is required")
	}
	stream, err := session.SubscribeEvents(ctx, controllercore.EventSubscriptionParams{
		WorkspaceID: params.WorkspaceID,
		SessionID:   params.SessionID,
		AfterSeq:    params.AfterSeq,
		ReplayLimit: params.ReplayLimit,
	})
	if err != nil {
		return remoteControlEventStream{}, fromCoreError(err)
	}
	out := make(chan AstralEvent, remoteControlStreamBufferSize)
	go func() {
		defer close(out)
		for envelope := range stream.Events {
			event, ok := envelope.Event.(AstralEvent)
			if !ok {
				continue
			}
			select {
			case out <- event:
			case <-ctx.Done():
				stream.Close()
				return
			}
		}
	}()
	return remoteControlEventStream{Events: out, Close: stream.Close}, nil
}

func (a *app) controllerCoreOpenTerminal(ctx context.Context, hostDeviceID, workspaceID string, afterSeq int64) (remoteHostTerminalStream, error) {
	core := a.controllerCoreManager()
	if core == nil {
		return nil, errors.New("controller core is not initialized")
	}
	session := core.OpenHostSession(hostDeviceID)
	if session == nil {
		return nil, errors.New("remote Host device id is required")
	}
	stream, err := session.OpenTerminal(ctx, workspaceID, afterSeq)
	if err != nil {
		return nil, fromCoreError(err)
	}
	return coreTerminalStreamAdapter{stream: stream}, nil
}

func (a *app) controllerCoreAttachTerminal(ctx context.Context, hostDeviceID, terminalID string, afterSeq int64) (remoteHostTerminalStream, error) {
	core := a.controllerCoreManager()
	if core == nil {
		return nil, errors.New("controller core is not initialized")
	}
	session := core.OpenHostSession(hostDeviceID)
	if session == nil {
		return nil, errors.New("remote Host device id is required")
	}
	stream, err := session.AttachTerminal(ctx, terminalID, afterSeq)
	if err != nil {
		return nil, fromCoreError(err)
	}
	return coreTerminalStreamAdapter{stream: stream}, nil
}

func (t daemonControllerTransport) MeshState(ctx context.Context, discover bool) (controllercore.MeshState, error) {
	if t.deps.meshState == nil {
		return controllercore.MeshState{}, controllercore.NewActionError(http.StatusServiceUnavailable, "daemon_unavailable", "daemon is not initialized")
	}
	state, err := t.deps.meshState(ctx, discover)
	if err != nil {
		return controllercore.MeshState{}, toCoreError(err)
	}
	return toCoreMeshState(state), nil
}

func (t daemonControllerTransport) RequestPairing(ctx context.Context, hostDeviceID string) (controllercore.PairingSignal, error) {
	if t.deps.requestPairing == nil {
		return controllercore.PairingSignal{}, controllercore.NewActionError(http.StatusServiceUnavailable, "daemon_unavailable", "daemon is not initialized")
	}
	return t.deps.requestPairing(ctx, hostDeviceID)
}

func (a *app) requestControllerPairing(ctx context.Context, hostDeviceID string) (controllercore.PairingSignal, error) {
	if a == nil || a.store == nil {
		return controllercore.PairingSignal{}, controllercore.NewActionError(http.StatusServiceUnavailable, "daemon_unavailable", "daemon is not initialized")
	}
	hostDeviceID = strings.TrimSpace(hostDeviceID)
	if hostDeviceID == "" {
		return controllercore.PairingSignal{}, controllercore.NewActionError(http.StatusBadRequest, "remote_host_required", "remote Host device id is required")
	}
	client, err := a.cloudClientFromSettings()
	if err != nil {
		return controllercore.PairingSignal{}, toCoreError(err)
	}
	reqCtx, cancel := context.WithTimeout(ctx, cloudSyncTimeout)
	defer cancel()
	_, relayURL, _, err := cloudRelayClientFromCloud(reqCtx, client)
	if err != nil {
		return controllercore.PairingSignal{}, controllercore.NewActionError(http.StatusBadGateway, "cloud_request_failed", err.Error())
	}
	self := a.store.hostInfo().Identity
	settings := a.currentSettings()
	if _, err := client.RegisterDevice(reqCtx, self, settings.RemoteControl.Enabled, true, relayURL); err != nil {
		return controllercore.PairingSignal{}, controllercore.NewActionError(http.StatusBadGateway, "cloud_request_failed", err.Error())
	}
	signal, err := client.SubmitPairingSignal(reqCtx, cloudPairingSignalInput{
		HostDeviceID:        hostDeviceID,
		ControllerDeviceID:  self.DeviceID,
		Scope:               TrustScopeFull,
		Capabilities:        normalizeCapabilities(self.Capabilities),
		WorkspaceExecPolicy: WorkspaceExecPolicyTrusted,
	})
	if err != nil {
		return controllercore.PairingSignal{}, controllercore.NewActionError(http.StatusBadGateway, "cloud_request_failed", err.Error())
	}
	return toCorePairingSignal(signal), nil
}

func (t daemonControllerTransport) ControlState(hostDeviceID string) controllercore.ControlState {
	if t.deps.managedTransport == nil {
		return controllercore.ControlState{State: controllercore.StateIdle}
	}
	manager := t.deps.managedTransport()
	if manager == nil {
		return controllercore.ControlState{State: controllercore.StateIdle}
	}
	return manager.ControlState(hostDeviceID)
}

func (t daemonControllerTransport) Request(ctx context.Context, hostDeviceID, capability, action string, params map[string]any) (controllercore.ControlResponse, error) {
	manager := t.deps.managedTransport()
	if manager == nil {
		return controllercore.ControlResponse{}, controllercore.NewActionError(http.StatusServiceUnavailable, "controller_unavailable", "controller transport is not initialized")
	}
	return manager.Request(ctx, hostDeviceID, capability, action, params)
}

func (t daemonControllerTransport) SubscribeEvents(ctx context.Context, hostDeviceID string, params controllercore.EventSubscriptionParams) (controllercore.EventStream, error) {
	manager := t.deps.managedTransport()
	if manager == nil {
		return controllercore.EventStream{}, controllercore.NewActionError(http.StatusServiceUnavailable, "controller_unavailable", "controller transport is not initialized")
	}
	return manager.SubscribeEvents(ctx, hostDeviceID, params)
}

func (t daemonControllerTransport) OpenTerminal(ctx context.Context, hostDeviceID, workspaceID string, afterSeq int64) (controllercore.TerminalStream, error) {
	manager := t.deps.managedTransport()
	if manager == nil {
		return nil, controllercore.NewActionError(http.StatusServiceUnavailable, "controller_unavailable", "controller transport is not initialized")
	}
	return manager.OpenTerminal(ctx, hostDeviceID, workspaceID, afterSeq)
}

func (t daemonControllerTransport) AttachTerminal(ctx context.Context, hostDeviceID, terminalID string, afterSeq int64) (controllercore.TerminalStream, error) {
	manager := t.deps.managedTransport()
	if manager == nil {
		return nil, controllercore.NewActionError(http.StatusServiceUnavailable, "controller_unavailable", "controller transport is not initialized")
	}
	return manager.AttachTerminal(ctx, hostDeviceID, terminalID, afterSeq)
}

func (t daemonControllerTransport) Invalidate(hostDeviceID, reason string) {
	if t.deps.managedTransport == nil {
		return
	}
	if manager := t.deps.managedTransport(); manager != nil {
		manager.Invalidate(hostDeviceID, reason)
	}
}

type daemonControllerFrameConn struct {
	conn controlClientFrameConn
}

func (d daemonControllerFrameConn) Close() error {
	if d.conn == nil {
		return nil
	}
	return d.conn.Close()
}

func (d daemonControllerFrameConn) WritePlain(frame controlwire.PlainFrame) error {
	if d.conn == nil {
		return errors.New("remote control frame connection is closed")
	}
	return d.conn.WritePlain(fromWirePlainFrame(frame))
}

func (d daemonControllerFrameConn) ReadPlain(timeout time.Duration) (controlwire.PlainFrame, error) {
	if d.conn == nil {
		return controlwire.PlainFrame{}, errors.New("remote control frame connection is closed")
	}
	frame, err := d.conn.ReadPlain(timeout)
	if err != nil {
		return controlwire.PlainFrame{}, err
	}
	return toWirePlainFrame(frame), nil
}

type coreTerminalStreamAdapter struct {
	stream controllercore.TerminalStream
}

func (c coreTerminalStreamAdapter) TerminalID() string {
	if c.stream == nil {
		return ""
	}
	return c.stream.TerminalID()
}

func (c coreTerminalStreamAdapter) ViewerID() string {
	if c.stream == nil {
		return ""
	}
	return c.stream.ViewerID()
}

func (c coreTerminalStreamAdapter) InputLeaseID() string {
	if c.stream == nil {
		return ""
	}
	return c.stream.InputLeaseID()
}

func (c coreTerminalStreamAdapter) Shell() string {
	if c.stream == nil {
		return ""
	}
	return c.stream.Shell()
}

func (c coreTerminalStreamAdapter) CWD() string {
	if c.stream == nil {
		return ""
	}
	return c.stream.CWD()
}

func (c coreTerminalStreamAdapter) OutputSeq() int64 {
	if c.stream == nil {
		return 0
	}
	return c.stream.OutputSeq()
}

func (c coreTerminalStreamAdapter) Frames() <-chan controlPlainFrame {
	out := make(chan controlPlainFrame, remoteControlStreamBufferSize)
	if c.stream == nil {
		close(out)
		return out
	}
	go func() {
		defer close(out)
		for frame := range c.stream.Frames() {
			out <- fromCoreTerminalFrame(frame)
		}
	}()
	return out
}

func (c coreTerminalStreamAdapter) Input(data string) error {
	if c.stream == nil {
		return errors.New("remote terminal is closed")
	}
	return fromCoreError(c.stream.Input(data))
}

func (c coreTerminalStreamAdapter) Resize(cols, rows int) error {
	if c.stream == nil {
		return errors.New("remote terminal is closed")
	}
	return fromCoreError(c.stream.Resize(cols, rows))
}

func (c coreTerminalStreamAdapter) AckHeartbeat(seq, renderedSeq int64) error {
	if c.stream == nil {
		return errors.New("remote terminal is closed")
	}
	return fromCoreError(c.stream.AckHeartbeat(seq, renderedSeq))
}

func (c coreTerminalStreamAdapter) Close() error {
	if c.stream == nil {
		return nil
	}
	return fromCoreError(c.stream.Close())
}

func (c coreTerminalStreamAdapter) Detach() error {
	if c.stream == nil {
		return nil
	}
	return fromCoreError(c.stream.Detach())
}

func toCoreResolvedTarget(hostDeviceID string, target controlClientTarget) controllercore.ResolvedTarget {
	return controllercore.ResolvedTarget{
		HostDeviceID: hostDeviceID,
		Transport:    remoteControlTransport(target),
		Timeout:      target.Timeout,
		HasRelay:     strings.TrimSpace(target.RelayClient.BaseURL) != "" && strings.TrimSpace(target.RelayClient.Token) != "",
	}
}

func remoteControlTransport(target controlClientTarget) string {
	if target.UseRelay {
		return remoteHostStatusRelay
	}
	if strings.TrimSpace(target.BaseURL) != "" {
		return remoteHostStatusLAN
	}
	return ""
}

func toWirePlainFrame(frame controlPlainFrame) controlwire.PlainFrame {
	return controlwire.PlainFrame{
		Type:          frame.Type,
		Request:       toWireControlRequestPtr(frame.Request),
		Response:      toWireControlResponsePtr(frame.Response),
		Event:         marshalRaw(frame.Event),
		Terminal:      marshalRaw(frame.Terminal),
		Media:         marshalRaw(frame.Media),
		WorkspaceFile: marshalRaw(frame.WorkspaceFile),
		Reason:        frame.Reason,
		Code:          frame.Code,
	}
}

func fromWirePlainFrame(frame controlwire.PlainFrame) controlPlainFrame {
	return controlPlainFrame{
		Type:          frame.Type,
		Request:       fromWireControlRequestPtr(frame.Request),
		Response:      fromWireControlResponsePtr(frame.Response),
		Event:         eventFrameFromRaw(frame.Event),
		Terminal:      terminalFrameFromRaw(frame.Terminal),
		Media:         mediaFrameFromRaw(frame.Media),
		WorkspaceFile: workspaceFileFrameFromRaw(frame.WorkspaceFile),
		Reason:        frame.Reason,
		Code:          frame.Code,
	}
}

func toWireControlRequestPtr(req *ControlRequest) *controlwire.ControlRequest {
	if req == nil {
		return nil
	}
	return &controlwire.ControlRequest{
		RequestID:          req.RequestID,
		ControllerDeviceID: req.ControllerDeviceID,
		Capability:         req.Capability,
		Action:             req.Action,
		Params:             req.Params,
	}
}

func fromWireControlRequestPtr(req *controlwire.ControlRequest) *ControlRequest {
	if req == nil {
		return nil
	}
	return &ControlRequest{
		RequestID:          req.RequestID,
		ControllerDeviceID: req.ControllerDeviceID,
		Capability:         req.Capability,
		Action:             req.Action,
		Params:             req.Params,
	}
}

func toWireControlResponsePtr(response *ControlResponse) *controlwire.ControlResponse {
	if response == nil {
		return nil
	}
	return &controlwire.ControlResponse{
		RequestID: response.RequestID,
		OK:        response.OK,
		Result:    response.Result,
		Error:     toWireControlError(response.Error),
	}
}

func fromWireControlResponsePtr(response *controlwire.ControlResponse) *ControlResponse {
	if response == nil {
		return nil
	}
	return &ControlResponse{
		RequestID: response.RequestID,
		OK:        response.OK,
		Result:    response.Result,
		Error:     fromWireControlError(response.Error),
	}
}

func toWireControlError(err *ControlError) *controlwire.ControlError {
	if err == nil {
		return nil
	}
	return &controlwire.ControlError{Status: err.Status, Code: err.Code, Message: err.Message}
}

func fromWireControlError(err *controlwire.ControlError) *ControlError {
	if err == nil {
		return nil
	}
	return &ControlError{Status: err.Status, Code: err.Code, Message: err.Message}
}

func marshalRaw(value any) json.RawMessage {
	if value == nil {
		return nil
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		if reflected.IsNil() {
			return nil
		}
	}
	body, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	if string(body) == "null" {
		return nil
	}
	return body
}

func eventFrameFromRaw(body json.RawMessage) *eventStreamFrame {
	if len(body) == 0 {
		return nil
	}
	var frame eventStreamFrame
	if err := json.Unmarshal(body, &frame); err != nil {
		return nil
	}
	return &frame
}

func terminalFrameFromRaw(body json.RawMessage) *terminalStreamFrame {
	if len(body) == 0 {
		return nil
	}
	var frame terminalStreamFrame
	if err := json.Unmarshal(body, &frame); err != nil {
		return nil
	}
	return &frame
}

func mediaFrameFromRaw(body json.RawMessage) *mediaStreamFrame {
	if len(body) == 0 {
		return nil
	}
	var frame mediaStreamFrame
	if err := json.Unmarshal(body, &frame); err != nil {
		return nil
	}
	return &frame
}

func workspaceFileFrameFromRaw(body json.RawMessage) *workspaceFileStreamFrame {
	if len(body) == 0 {
		return nil
	}
	var frame workspaceFileStreamFrame
	if err := json.Unmarshal(body, &frame); err != nil {
		return nil
	}
	return &frame
}

func toCoreControlResponse(response ControlResponse) controllercore.ControlResponse {
	return controllercore.ControlResponse{
		RequestID: response.RequestID,
		OK:        response.OK,
		Result:    response.Result,
		Error:     toCoreControlError(response.Error),
	}
}

func toCoreControlError(err *ControlError) *controllercore.ControlError {
	if err == nil {
		return nil
	}
	return &controllercore.ControlError{Status: err.Status, Code: err.Code, Message: err.Message}
}

func toCoreError(err error) error {
	if err == nil {
		return nil
	}
	var actionErr *actionError
	if !errors.As(err, &actionErr) {
		return err
	}
	code := actionErr.Code
	if code == terminalViewerNotReadyCode {
		code = controllercore.TerminalViewerNotReadyCode
	}
	return controllercore.NewActionError(actionErr.Status, code, actionErr.Message)
}

func fromCoreControlResponse(response controllercore.ControlResponse) ControlResponse {
	return ControlResponse{
		RequestID: response.RequestID,
		OK:        response.OK,
		Result:    response.Result,
		Error:     fromCoreControlError(response.Error),
	}
}

func fromCoreControlError(err *controllercore.ControlError) *ControlError {
	if err == nil {
		return nil
	}
	return &ControlError{Status: err.Status, Code: err.Code, Message: err.Message}
}

func fromCoreError(err error) error {
	if err == nil {
		return nil
	}
	var coreErr *controllercore.ActionError
	if !errors.As(err, &coreErr) {
		return err
	}
	code := coreErr.Code
	if code == controllercore.TerminalViewerNotReadyCode {
		code = terminalViewerNotReadyCode
	}
	return newActionError(coreErr.Status, code, coreErr.Message)
}

func toCoreMeshState(state meshStateResponse) controllercore.MeshState {
	return controllercore.MeshState{
		Self:                toCoreMeshSelfState(state.Self),
		Cloud:               toCoreMeshCloudState(state.Cloud),
		Hosts:               toCoreRemoteHostRecords(state.Hosts),
		PendingPairingCount: state.PendingPairingCount,
		UpdatedAt:           state.UpdatedAt,
	}
}

func toCoreMeshSelfState(state meshSelfState) controllercore.MeshSelfState {
	return controllercore.MeshSelfState{
		DeviceID:       state.DeviceID,
		DeviceName:     state.DeviceName,
		CanHost:        state.CanHost,
		CanControl:     state.CanControl,
		CloudActive:    state.CloudActive,
		RelayConnected: state.RelayConnected,
	}
}

func toCoreMeshCloudState(state *meshCloudState) *controllercore.MeshCloudState {
	if state == nil {
		return nil
	}
	return &controllercore.MeshCloudState{
		Enabled:             state.Enabled,
		AccountIDHash:       state.AccountIDHash,
		RelayID:             state.RelayID,
		RelayURL:            state.RelayURL,
		CredentialExpiresAt: state.CredentialExpiresAt,
	}
}

func toCoreRemoteHostRecords(hosts []remoteHostRecord) []controllercore.RemoteHostRecord {
	out := make([]controllercore.RemoteHostRecord, 0, len(hosts))
	for _, host := range hosts {
		out = append(out, toCoreRemoteHostRecord(host))
	}
	return out
}

func toCoreRemoteHostRecord(host remoteHostRecord) controllercore.RemoteHostRecord {
	return controllercore.RemoteHostRecord{
		DeviceID:             host.DeviceID,
		DeviceName:           host.DeviceName,
		DeviceKind:           host.DeviceKind,
		PublicKeyFingerprint: host.PublicKeyFingerprint,
		KnownIdentity:        host.KnownIdentity,
		Status:               host.Status,
		Connection:           host.Connection,
		AuthorizationState:   host.AuthorizationState,
		PairingRequestID:     host.PairingRequestID,
		PairingStatus:        host.PairingStatus,
		LastBaseURL:          host.LastBaseURL,
		LANBaseURL:           host.LANBaseURL,
		Capabilities:         append([]string(nil), host.Capabilities...),
		Control:              host.Control,
	}
}

func toCorePairingSignal(signal CloudPairingSignal) controllercore.PairingSignal {
	return controllercore.PairingSignal{
		RequestID:                      signal.RequestID,
		AccountIDHash:                  signal.AccountIDHash,
		HostDeviceID:                   signal.HostDeviceID,
		HostDeviceName:                 signal.HostDeviceName,
		HostDeviceKind:                 signal.HostDeviceKind,
		HostPublicKeyFingerprint:       signal.HostPublicKeyFingerprint,
		ControllerDeviceID:             signal.ControllerDeviceID,
		ControllerDeviceName:           signal.ControllerDeviceName,
		ControllerDeviceKind:           signal.ControllerDeviceKind,
		ControllerPublicKeyFingerprint: signal.ControllerPublicKeyFingerprint,
		Scope:                          signal.Scope,
		Status:                         signal.Status,
		Capabilities:                   append([]string(nil), signal.Capabilities...),
		WorkspaceExecPolicy:            signal.WorkspaceExecPolicy,
		ResolverDeviceID:               signal.ResolverDeviceID,
		CreatedAt:                      signal.CreatedAt,
		UpdatedAt:                      signal.UpdatedAt,
		ResolvedAt:                     signal.ResolvedAt,
	}
}

func fromCoreTerminalFrame(frame controllercore.TerminalFrame) controlPlainFrame {
	return controlPlainFrame{
		Type:     frame.Type,
		Response: fromCoreControlResponsePtr(frame.Response),
		Terminal: fromCoreTerminalPayload(frame.Type, frame.Terminal),
	}
}

func fromCoreControlResponsePtr(response *controllercore.ControlResponse) *ControlResponse {
	if response == nil {
		return nil
	}
	next := fromCoreControlResponse(*response)
	return &next
}

func fromCoreTerminalPayload(frameType string, payload *controllercore.TerminalPayload) *terminalStreamFrame {
	if payload == nil {
		return nil
	}
	return &terminalStreamFrame{
		frameType:    frameType,
		TerminalID:   payload.TerminalID,
		WorkspaceID:  payload.WorkspaceID,
		Target:       payload.Target,
		Status:       payload.Status,
		OutputSeq:    payload.OutputSeq,
		ViewerID:     payload.ViewerID,
		InputLeaseID: payload.InputLeaseID,
		HeartbeatSeq: payload.HeartbeatSeq,
		RenderedSeq:  payload.RenderedSeq,
		Data:         payload.Data,
		Cols:         uint16(payload.Cols),
		Rows:         uint16(payload.Rows),
		Reason:       payload.Reason,
		Code:         payload.Code,
		CanInput:     payload.CanInput,
	}
}
