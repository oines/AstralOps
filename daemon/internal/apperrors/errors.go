package apperrors

type ActionError struct {
	Status  int
	Code    string
	Message string
}

func (e *ActionError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func New(status int, code string, message string) *ActionError {
	return &ActionError{Status: status, Code: code, Message: message}
}
