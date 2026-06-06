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
		writeJSON(w, actionErr.Status, map[string]any{
			"error":   actionErr.Message,
			"code":    actionErr.Code,
			"message": actionErr.Message,
			"details": actionErr.Details,
		})
		return
	}
	writeJSON(w, http.StatusInternalServerError, map[string]any{
		"error":   err.Error(),
		"code":    "internal_error",
		"message": err.Error(),
	})
}
