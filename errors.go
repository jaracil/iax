package iax

import (
	"errors"
)

var (
	ErrInvalidFrame          = errors.New("invalid frame")
	ErrTimeout               = errors.New("timeout")
	ErrUnexpectedFrameType   = errors.New("unexpected frame type")
	ErrMissingIE             = errors.New("missing information element")
	ErrUnsupportedAuthMethod = errors.New("unsupported authentication method")
	ErrConnectionRejected    = errors.New("connection rejected")
	ErrInvalidState          = errors.New("invalid state")
)
