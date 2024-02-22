package iax

import (
	"errors"
)

var (
	ErrInvalidFrame          = errors.New("invalid frame")
	ErrTimeout               = errors.New("timeout")
	ErrInternal              = errors.New("internal error")
	ErrUnexpectedFrameType   = errors.New("unexpected frame type")
	ErrMissingIE             = errors.New("missing information element")
	ErrUnsupportedAuthMethod = errors.New("unsupported authentication method")
	ErrRejected              = errors.New("rejected")
	ErrInvalidState          = errors.New("invalid state")
	ErrAuthFailed            = errors.New("authentication failed")
	ErrResourceBusy          = errors.New("resource busy")
	ErrLocalHangup           = errors.New("local hangup")
	ErrRemoteHangup          = errors.New("remote hangup")
)
