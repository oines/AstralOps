package apperrors

import "github.com/oines/astralops/pkg/protocol"

type ActionError struct {
	Status  int
	Code    protocol.ControlErrorCode
	Message string
	Details map[string]string
}

func (e *ActionError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func New(status int, code string, message string) *ActionError {
	return &ActionError{Status: status, Code: protocol.ControlErrorCode(code), Message: message}
}
