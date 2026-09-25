package meta

import "fmt"

// Code is a stable error code of docs/meta-api.md. Messages are for humans and may change.
type Code string

const (
	ErrInvalidRef        Code = "invalid-ref"
	ErrInvalidName       Code = "invalid-name"
	ErrInvalidID         Code = "invalid-id"
	ErrUnknownObject     Code = "unknown-object"
	ErrUnknownEnum       Code = "unknown-enum"
	ErrUnknownPath       Code = "unknown-path"
	ErrDuplicateID       Code = "duplicate-id"
	ErrDuplicatePath     Code = "duplicate-path"
	ErrHasMembers        Code = "has-members"
	ErrInvalidMove       Code = "invalid-move"
	ErrTooDeep           Code = "too-deep"
	ErrFormatUnsupported Code = "format-unsupported"
	ErrRevisionConflict  Code = "revision-conflict"
	ErrForbidden         Code = "forbidden"
	ErrInvalidBody       Code = "invalid-body"
)

// Error is a store error with a stable code and optional structured detail.
type Error struct {
	Code    Code
	Message string
	Detail  map[string]any
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

func errf(code Code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// HTTPStatus maps a code to the status docs/meta-api.md prescribes.
func (c Code) HTTPStatus() int {
	switch c {
	case ErrUnknownObject, ErrUnknownEnum:
		return 404
	case ErrDuplicateID, ErrHasMembers, ErrRevisionConflict:
		return 409
	case ErrForbidden:
		return 403
	case ErrUnknownPath:
		return 422
	default:
		return 422
	}
}
