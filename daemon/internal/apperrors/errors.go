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

func (e *ActionError) ControlError() *protocol.ControlError {
	if e == nil {
		return nil
	}
	return &protocol.ControlError{
		Status:  e.Status,
		Code:    e.Code,
		Message: e.Message,
		Details: e.Details,
	}
}

func New(status int, code string, message string) *ActionError {
	return &ActionError{Status: status, Code: protocol.ControlErrorCode(code), Message: message}
}
