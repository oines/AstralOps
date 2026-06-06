package protocol

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestControlActionSpecsHaveCapabilities(t *testing.T) {
	specs := ControlActionSpecs()
	if len(specs) == 0 {
		t.Fatal("ControlActionSpecs is empty")
	}
	seen := map[ControlAction]bool{}
	for _, spec := range specs {
		if spec.Action == "" {
			t.Fatalf("spec has empty action: %#v", spec)
		}
		if spec.Capability == "" {
			t.Fatalf("spec %s has empty capability", spec.Action)
		}
		if seen[spec.Action] {
			t.Fatalf("duplicate action spec: %s", spec.Action)
		}
		seen[spec.Action] = true
		if got := RequiredCapability(spec.Action); got != spec.Capability {
			t.Fatalf("RequiredCapability(%s) = %s, want %s", spec.Action, got, spec.Capability)
		}
	}
}

func TestParseControlActionRejectsUnknown(t *testing.T) {
	if _, ok := ParseControlAction("core.control.not_real"); ok {
		t.Fatal("ParseControlAction accepted unknown action")
	}
	if action, ok := ParseControlAction(string(ControlActionSessionInput)); !ok || action != ControlActionSessionInput {
		t.Fatalf("ParseControlAction session input = %q %v", action, ok)
	}
}

func TestValidateControlRequestActionChecksCapability(t *testing.T) {
	err := ValidateControlRequestAction(ControlRequest{
		Capability: CapabilityCoreRead,
		Action:     ControlActionSessionInput,
	})
	var actionErr *ActionError
	if !errors.As(err, &actionErr) {
		t.Fatalf("err = %#v, want ActionError", err)
	}
	if actionErr.Code != ControlErrorCapabilityMismatch {
		t.Fatalf("code = %q, want %q", actionErr.Code, ControlErrorCapabilityMismatch)
	}
}

func TestDecodeControlParamsRejectsUnknownFields(t *testing.T) {
	var params QueueControlParams
	raw, err := MarshalControlParams(map[string]any{
		"session_id": "sess_1",
		"queue_id":   "queue_1",
		"extra":      true,
	})
	if err != nil {
		t.Fatalf("MarshalControlParams err = %v", err)
	}
	err = DecodeControlParamsInto(ControlActionQueueCancel, raw, &params)
	var actionErr *ActionError
	if !errors.As(err, &actionErr) {
		t.Fatalf("err = %#v, want ActionError", err)
	}
	if actionErr.Code != ControlErrorInvalidParams {
		t.Fatalf("code = %q, want %q", actionErr.Code, ControlErrorInvalidParams)
	}
}

func TestNewControlRequestKeepsParamsAsRawJSONObject(t *testing.T) {
	req, err := NewControlRequest(CapabilityCoreControl, ControlActionQueueCancel, QueueControlParams{
		SessionID: "sess_1",
		QueueID:   "queue_1",
	})
	if err != nil {
		t.Fatalf("NewControlRequest err = %v", err)
	}
	if req.Capability != CapabilityCoreControl || req.Action != ControlActionQueueCancel {
		t.Fatalf("request = %#v, want typed action/capability", req)
	}
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal err = %v", err)
	}
	if want := `"params":{"session_id":"sess_1","queue_id":"queue_1"}`; !containsJSONFragment(string(body), want) {
		t.Fatalf("request JSON = %s, want params object fragment %s", string(body), want)
	}
}

func containsJSONFragment(value, fragment string) bool {
	for index := 0; index+len(fragment) <= len(value); index++ {
		if value[index:index+len(fragment)] == fragment {
			return true
		}
	}
	return false
}
