package iax

import (
	"errors"
)

var (
	// ErrInvalidFrame indicates a malformed or corrupted IAX2 frame was received
	ErrInvalidFrame          = errors.New("invalid frame")
	// ErrTimeout indicates an operation exceeded its allowed time limit
	ErrTimeout               = errors.New("timeout")
	// ErrInternal indicates an unexpected internal error occurred
	ErrInternal              = errors.New("internal error")
	// ErrUnexpectedFrame indicates a frame was received that doesn't match the expected protocol flow
	ErrUnexpectedFrame       = errors.New("unexpected frame type")
	// ErrMissingIE indicates a required Information Element was not present in the frame
	ErrMissingIE             = errors.New("missing information element")
	// ErrUnsupportedAuthMethod indicates the peer requested an authentication method that is not supported
	ErrUnsupportedAuthMethod = errors.New("unsupported authentication method")
	// ErrLocalReject indicates the local side rejected the call or operation
	ErrLocalReject           = errors.New("local rejecte")
	// ErrRemoteReject indicates the remote peer rejected the call or operation
	ErrRemoteReject          = errors.New("remote reject")
	// ErrInvalidState indicates the operation is not valid in the current call or trunk state
	ErrInvalidState          = errors.New("invalid state")
	// ErrAuthFailed indicates authentication credentials were incorrect or authentication failed
	ErrAuthFailed            = errors.New("authentication failed")
	// ErrResourceBusy indicates the requested resource is currently in use and cannot be accessed
	ErrResourceBusy          = errors.New("resource busy")
	// ErrLocalHangup indicates the call was terminated by the local side
	ErrLocalHangup           = errors.New("local hangup")
	// ErrRemoteHangup indicates the call was terminated by the remote peer
	ErrRemoteHangup          = errors.New("remote hangup")
	// ErrPeerNotFound indicates the specified peer is not registered or configured
	ErrPeerNotFound          = errors.New("peer not found")
	// ErrPeerUnreachable indicates the peer cannot be contacted (no address or network error)
	ErrPeerUnreachable       = errors.New("peer unreachable")
	// ErrFullBuffer indicates a queue or buffer is at capacity and cannot accept more data
	ErrFullBuffer            = errors.New("full buffer")
)
