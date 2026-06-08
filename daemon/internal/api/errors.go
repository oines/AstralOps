package api

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/oines/astralops/pkg/protocol"
)

type controlErrorProvider interface {
	ControlError() *protocol.ControlError
}

func decodeJSON(r io.Reader, v any) error {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeDecodeError(w http.ResponseWriter, err error) {
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
}

func writeActionError(w http.ResponseWriter, err error) {
	controlErr := controlError(err)
	status := controlErr.Status
	if status == 0 {
		status = http.StatusInternalServerError
	}
	writeJSON(w, status, map[string]any{
		"error":   controlErr.Message,
		"code":    controlErr.Code,
		"message": controlErr.Message,
		"details": controlErr.Details,
	})
}

func controlError(err error) *protocol.ControlError {
	if err == nil {
		return &protocol.ControlError{Status: http.StatusInternalServerError, Code: "internal_error", Message: "unknown error"}
	}
	if provider, ok := err.(controlErrorProvider); ok {
		if controlErr := provider.ControlError(); controlErr != nil {
			return controlErr
		}
	}
	return protocol.ControlErrorFromError(err)
}
