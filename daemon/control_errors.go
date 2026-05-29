package main

import (
	"errors"
	"net/http"
)

type actionError struct {
	Status  int
	Code    string
	Message string
}

func (e *actionError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func newActionError(status int, code string, message string) *actionError {
	return &actionError{Status: status, Code: code, Message: message}
}

func writeActionError(w http.ResponseWriter, err error) {
	var actionErr *actionError
	if errors.As(err, &actionErr) {
		writeJSON(w, actionErr.Status, map[string]string{"error": actionErr.Message})
		return
	}
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
}
