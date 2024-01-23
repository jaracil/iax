package iax

import (
	"errors"
)

var (
	ErrInvalidFrame        = errors.New("invalid frame")
	ErrTimeout             = errors.New("timeout")
	ErrUnexpectedFrameType = errors.New("unexpected frame type")
)
