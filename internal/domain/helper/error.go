package helper

import "net/http"

const (
	MsgInternalServerError = "Internal Server Error"
	MsgServiceUnavailable  = "Service Unavailable"
	MsgBadRequest          = "Bad Request"
	MsgNotFound            = "Not Found"
	MsgUnauthorized        = "Unauthorized"
	MsgForbidden           = "Forbidden"
	MsgMethodNotAllowed    = "Method Not Allowed"
	MsgTooManyRequests     = "Too Many Requests"
	MsgConflict            = "Conflict"
)

type AppError struct {
	Code    int
	Message string
}

func (e *AppError) Error() string {
	return e.Message
}

func NewAppError(code int, message string) *AppError {
	return &AppError{
		Code:    code,
		Message: message,
	}
}

func NewBadRequestError(message string) *AppError {
	if message == "" {
		message = MsgBadRequest
	}
	return NewAppError(http.StatusBadRequest, message)
}

func NewInternalServerError(message string) *AppError {
	if message == "" {
		message = MsgInternalServerError
	}
	return NewAppError(http.StatusInternalServerError, message)
}

func NewServiceUnavailableError(message string) *AppError {
	if message == "" {
		message = MsgServiceUnavailable
	}
	return NewAppError(http.StatusServiceUnavailable, message)
}

func NewNotFoundError(message string) *AppError {
	if message == "" {
		message = MsgNotFound
	}
	return NewAppError(http.StatusNotFound, message)
}

func NewUnauthorizedError(message string) *AppError {
	if message == "" {
		message = MsgUnauthorized
	}
	return NewAppError(http.StatusUnauthorized, message)
}

func NewForbiddenError(message string) *AppError {
	if message == "" {
		message = MsgForbidden
	}
	return NewAppError(http.StatusForbidden, message)
}

func NewMethodNotAllowedError(message string) *AppError {
	if message == "" {
		message = MsgMethodNotAllowed
	}
	return NewAppError(http.StatusMethodNotAllowed, message)
}

func NewTooManyRequestsError(message string) *AppError {
	if message == "" {
		message = MsgTooManyRequests
	}
	return NewAppError(http.StatusTooManyRequests, message)
}

func NewConflictError(message string) *AppError {
	if message == "" {
		message = MsgConflict
	}
	return NewAppError(http.StatusConflict, message)
}
