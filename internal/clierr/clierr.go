// Package clierr classifies errors into the documented exit codes.
package clierr

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"

	"github.com/mattermost/mattermost/server/public/model"
)

// Exit codes.
const (
	CodeOK          = 0
	CodeError       = 1
	CodeUsage       = 2
	CodeAuth        = 3
	CodeForbidden   = 4
	CodeNotFound    = 5
	CodeRateLimited = 6
	CodeNetwork     = 7
	// CodeInterrupted and CodeTerminated follow the shell convention of
	// 128 plus the signal number (SIGINT 2, SIGTERM 15).
	CodeInterrupted = 130
	CodeTerminated  = 143
)

// Error carries an exit code with a user-facing message.
type Error struct {
	Code int
	Msg  string
	Err  error
}

func (e *Error) Error() string {
	if e.Msg != "" {
		return e.Msg
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return "error"
}

func (e *Error) Unwrap() error { return e.Err }

// New returns an error with the given code.
func New(code int, format string, args ...any) error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}

// Wrap attaches a code and message to err.
func Wrap(code int, err error, format string, args ...any) error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...), Err: err}
}

// Usage reports a wrong command line.
func Usage(format string, args ...any) error {
	return New(CodeUsage, format, args...)
}

// Interrupted is the cancel cause of a run stopped by SIGINT (Ctrl+C).
func Interrupted() error { return New(CodeInterrupted, "interrupted") }

// Terminated is the cancel cause of a run stopped by SIGTERM.
func Terminated() error { return New(CodeTerminated, "terminated") }

// API classifies an error returned by Client4. The status comes from the
// response first: Client4 returns a non-AppError when the body is not JSON.
func API(resp *model.Response, err error) error {
	if err == nil {
		return nil
	}
	// An error already classified, such as the transport's refusal to wait
	// out a rate-limit ban, is returned bare: Client4 wraps it in a
	// *url.Error whose text would prefix the message with the request.
	var ce *Error
	if errors.As(err, &ce) {
		return ce
	}
	msg := err.Error()
	status := 0
	if resp != nil {
		status = resp.StatusCode
	}
	var ae *model.AppError
	if errors.As(err, &ae) {
		if ae.Message != "" {
			msg = ae.Message
		}
		if status == 0 {
			status = ae.StatusCode
		}
	}
	switch status {
	case http.StatusUnauthorized:
		return Wrap(CodeAuth, err, "session expired or invalid; run `mm-cli auth login`")
	case http.StatusForbidden:
		return Wrap(CodeForbidden, err, "permission denied: %s", msg)
	case http.StatusNotFound:
		return Wrap(CodeNotFound, err, "not found: %s", msg)
	case http.StatusTooManyRequests:
		return Wrap(CodeRateLimited, err, "rate limited by the server; try again later")
	}
	if errors.Is(err, context.Canceled) {
		return Wrap(CodeError, err, "canceled")
	}
	var ne net.Error
	if errors.As(err, &ne) {
		return Wrap(CodeNetwork, err, "network error: %s", msg)
	}
	return Wrap(CodeError, err, "%s", msg)
}

// ExitCode maps an error to the process exit code.
func ExitCode(err error) int {
	if err == nil {
		return CodeOK
	}
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return CodeError
}
