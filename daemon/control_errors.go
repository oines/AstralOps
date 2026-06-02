package main

import (
	"errors"
	"net/http"

	"github.com/oines/astralops/daemon/internal/apperrors"
)

const controlAuthorizationRequiredCode = "control_authorization_required"

type actionError = apperrors.ActionError

func newActionError(status int, code string, message string) *actionError {
	return apperrors.New(status, code, message)
}

func writeActionError(w http.ResponseWriter, err error) {
	var actionErr *actionError
	if errors.As(err, &actionErr) {
		writeJSON(w, actionErr.Status, map[string]string{"error": actionErr.Message})
		return
	}
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
}
