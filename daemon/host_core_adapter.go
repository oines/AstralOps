package main

import (
	"context"
	"crypto/ed25519"
	"errors"
	"net/http"

	"github.com/oines/astralops/pkg/controlwire"
	"github.com/oines/astralops/pkg/hostcore"
)

func (a *app) hostCoreManager() *hostcore.Core {
	if a == nil {
		return nil
	}
	if !a.hostRoleEnabled() {
		return nil
	}
	a.controlMu.Lock()
	defer a.controlMu.Unlock()
	if a.hostCore == nil {
		a.hostCore = hostcore.New(hostcore.Adapters{
			Identity:     hostCoreIdentityAdapterFromApp(a),
			Membership:   hostCoreMembershipAdapterFromApp(a),
			Trust:        hostCoreTrustAdapterFromApp(a),
			Capabilities: hostcore.CapabilityResolverFunc(controlActionCapability),
			Dispatcher:   hostCoreDispatchAdapter{control: a.remoteControlService(), store: a.store},
		})
	}
	return a.hostCore
}

type hostCoreIdentityAdapter struct {
	hostIdentity func() (DeviceIdentity, error)
	signHost     func([]byte) ([]byte, error)
}

func hostCoreIdentityAdapterFromApp(a *app) hostCoreIdentityAdapter {
	var st *store
	if a != nil {
		st = a.store
	}
	return hostCoreIdentityAdapter{
		hostIdentity: func() (DeviceIdentity, error) {
			if st == nil {
				return DeviceIdentity{}, errors.New("host store is not initialized")
			}
			return st.hostInfo().Identity, nil
		},
		signHost: func(payload []byte) ([]byte, error) {
			if st == nil || len(st.devicePrivateKey) != ed25519.PrivateKeySize {
				return nil, errors.New("host private key is not initialized")
			}
			return ed25519.Sign(ed25519.PrivateKey(st.devicePrivateKey), payload), nil
		},
	}
}

func (p hostCoreIdentityAdapter) HostIdentity(context.Context) (DeviceIdentity, error) {
	if p.hostIdentity == nil {
		return DeviceIdentity{}, errors.New("host store is not initialized")
	}
	return p.hostIdentity()
}

func (p hostCoreIdentityAdapter) SignHost(_ context.Context, payload []byte) ([]byte, error) {
	if p.signHost == nil {
		return nil, errors.New("host private key is not initialized")
	}
	return p.signHost(payload)
}

type hostCoreMembershipAdapter struct {
	currentMembership func(cloudMembershipRole) (cloudMembershipState, error)
}

func hostCoreMembershipAdapterFromApp(a *app) hostCoreMembershipAdapter {
	var st *store
	if a != nil {
		st = a.store
	}
	return hostCoreMembershipAdapter{
		currentMembership: func(role cloudMembershipRole) (cloudMembershipState, error) {
			if st == nil {
				return cloudMembershipState{}, cloudMeshInactiveError()
			}
			return st.currentCloudMembership(role)
		},
	}
}

func (p hostCoreMembershipAdapter) HostMembership(context.Context) (controlwire.MembershipState, error) {
	if p.currentMembership == nil {
		return controlwire.MembershipState{}, cloudMeshInactiveError()
	}
	membership, err := p.currentMembership(cloudMembershipRole{CanHost: true})
	if err != nil {
		return controlwire.MembershipState{}, err
	}
	return controlwire.MembershipState{
		AccountIDHash:    membership.AccountIDHash,
		SigningPublicKey: membership.SigningPublicKey,
		Lease:            membership.Lease,
	}, nil
}

type hostCoreTrustAdapter struct {
	trustedController func(string) (TrustGrant, bool)
}

func hostCoreTrustAdapterFromApp(a *app) hostCoreTrustAdapter {
	var st *store
	if a != nil {
		st = a.store
	}
	return hostCoreTrustAdapter{
		trustedController: func(controllerDeviceID string) (TrustGrant, bool) {
			if st == nil {
				return TrustGrant{}, false
			}
			return st.trustedControlGrant(controllerDeviceID)
		},
	}
}

func (p hostCoreTrustAdapter) TrustedController(_ context.Context, controllerDeviceID string) (hostcore.TrustGrant, bool, error) {
	if p.trustedController == nil {
		return hostcore.TrustGrant{}, false, errors.New("host store is not initialized")
	}
	grant, ok := p.trustedController(controllerDeviceID)
	if !ok {
		return hostcore.TrustGrant{}, false, nil
	}
	return toHostCoreTrustGrant(grant), true, nil
}

type hostCoreDispatchAdapter struct {
	control *remoteControlService
	store   *store
}

func (p hostCoreDispatchAdapter) DispatchControlRequest(ctx context.Context, session hostcore.Session, req ControlRequest) (ControlResponse, error) {
	if p.control == nil || p.store == nil {
		return ControlResponse{RequestID: req.RequestID}, newActionError(http.StatusServiceUnavailable, "host_service_unavailable", "host store is not initialized")
	}
	conn, _ := session.Connection.(controlConnection)
	grant, ok := p.store.trustedControlGrant(req.ControllerDeviceID)
	if !ok {
		return ControlResponse{RequestID: req.RequestID}, newActionError(http.StatusForbidden, "capability_denied", "controller is not allowed to use capability")
	}
	return p.control.executeAuthorizedControlRequestWithContext(ctx, req, conn, grant)
}

func toHostCoreTrustGrant(grant TrustGrant) hostcore.TrustGrant {
	return hostcore.TrustGrant{
		HostDeviceID:                   grant.HostDeviceID,
		ControllerDeviceID:             grant.ControllerDeviceID,
		ControllerPublicKey:            grant.ControllerPublicKey,
		ControllerPublicKeyFingerprint: grant.ControllerPublicKeyFingerprint,
		Capabilities:                   append([]string(nil), grant.Capabilities...),
		WorkspaceExecPolicy:            grant.WorkspaceExecPolicy,
		Revoked:                        grant.RevokedAt != "" || grant.Status == TrustStatusRevoked,
	}
}

func controlHelloCloseFrame(err error) controlPlainFrame {
	code := "handshake_failed"
	reason := "remote control handshake rejected"
	var coreErr *hostcore.Error
	if errors.As(err, &coreErr) {
		code = coreErr.Code
		reason = coreErr.Message
	}
	var actionErr *actionError
	if errors.As(err, &actionErr) {
		code = actionErr.Code
		reason = actionErr.Message
	}
	if code == hostcore.ErrorCodeControllerUntrusted {
		code = "capability_denied"
		reason = "controller is not trusted"
	}
	return controlPlainFrame{Type: "close", Code: code, Reason: reason}
}

func controlHelloCloseCode(err error) string {
	return controlHelloCloseFrame(err).Code
}

func controlHelloCloseReason(err error) string {
	return controlHelloCloseFrame(err).Reason
}

func hostCoreCipher(cipher *controlwire.Cipher) *controlCipher {
	if cipher == nil {
		return nil
	}
	return &controlCipher{inner: cipher}
}
