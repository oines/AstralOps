package agents

import (
	"context"
	"testing"

	"github.com/oines/astralops/daemon/internal/sessiontypes"
	"github.com/oines/astralops/pkg/protocol"
)

func TestLegacyAdapterStartsAndInterruptsRuntime(t *testing.T) {
	legacy := &fakeLegacyRuntime{}
	runtime := AdaptLegacy(legacy)
	if runtime == nil {
		t.Fatal("AdaptLegacy = nil, want runtime")
	}
	events, err := runtime.StartTurn(context.Background(), TurnRequest{
		Session:   protocol.Session{ID: "sess", WorkspaceID: "ws", Agent: protocol.AgentCodex},
		Workspace: protocol.Workspace{ID: "ws"},
		Input:     "hello",
		Options:   sessiontypes.TurnOptions{Model: "gpt-test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := <-events; ok {
		t.Fatal("events channel is open, want closed compatibility stream")
	}
	if legacy.startedInput != "hello" || legacy.startedOptions.Model != "gpt-test" {
		t.Fatalf("start = %q/%q, want hello/gpt-test", legacy.startedInput, legacy.startedOptions.Model)
	}
	if err := runtime.Interrupt(context.Background(), "sess"); err != nil {
		t.Fatal(err)
	}
	if legacy.interrupted != "sess" {
		t.Fatalf("interrupted = %q, want sess", legacy.interrupted)
	}
}

func TestLegacyAdapterRespondsApprovalWhenSupported(t *testing.T) {
	legacy := &fakeLegacyRuntime{}
	runtime := AdaptLegacy(legacy)
	if err := runtime.RespondInteraction(context.Background(), InteractionResponse{
		InteractionID: "approval_1",
		Response:      map[string]any{"decision": "accept"},
	}); err != nil {
		t.Fatal(err)
	}
	if legacy.approvalID != "approval_1" || legacy.approvalResponse["decision"] != "accept" {
		t.Fatalf("approval = %q/%#v, want approval_1 accept", legacy.approvalID, legacy.approvalResponse)
	}
}

type fakeLegacyRuntime struct {
	startedInput     string
	startedOptions   sessiontypes.TurnOptions
	interrupted      string
	approvalID       string
	approvalResponse map[string]any
}

func (f *fakeLegacyRuntime) StartTurn(_ protocol.Session, _ protocol.Workspace, input string, options sessiontypes.TurnOptions) error {
	f.startedInput = input
	f.startedOptions = options
	return nil
}

func (f *fakeLegacyRuntime) Interrupt(sessionID string) error {
	f.interrupted = sessionID
	return nil
}

func (f *fakeLegacyRuntime) RespondApproval(approvalID string, response map[string]any) error {
	f.approvalID = approvalID
	f.approvalResponse = response
	return nil
}
