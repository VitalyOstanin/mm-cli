package clierr

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
)

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

func TestAPI(t *testing.T) {
	appErr := &model.AppError{Message: "boom"}
	tests := []struct {
		name string
		resp *model.Response
		err  error
		code int
		msg  string
	}{
		{"nil", nil, nil, CodeOK, ""},
		{"401", &model.Response{StatusCode: 401}, appErr, CodeAuth, "mm-cli auth login"},
		{"403", &model.Response{StatusCode: 403}, appErr, CodeForbidden, "permission denied: boom"},
		{"404", &model.Response{StatusCode: 404}, appErr, CodeNotFound, "not found: boom"},
		{"429", &model.Response{StatusCode: 429}, appErr, CodeRateLimited, "rate limited"},
		{"500", &model.Response{StatusCode: 500}, appErr, CodeError, "boom"},
		{"status from AppError", nil, &model.AppError{Message: "gone", StatusCode: 404}, CodeNotFound, "gone"},
		{"network", nil, &url.Error{Op: "Get", URL: "https://chat.example.com", Err: timeoutErr{}}, CodeNetwork, "network error"},
		{"canceled", nil, context.Canceled, CodeError, "canceled"},
		{"non-JSON body", &model.Response{StatusCode: 502}, errors.New("failed to decode JSON payload"), CodeError, "decode"},
		{"already classified", nil, Usage("bad"), CodeUsage, "bad"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := API(tt.resp, tt.err)
			if got := ExitCode(err); got != tt.code {
				t.Fatalf("code = %d, want %d (err %v)", got, tt.code, err)
			}
			if tt.msg != "" && !strings.Contains(err.Error(), tt.msg) {
				t.Fatalf("message %q does not contain %q", err.Error(), tt.msg)
			}
		})
	}
}

func TestExitCodeWrapped(t *testing.T) {
	err := fmt.Errorf("context: %w", Usage("bad flag %s", "-x"))
	if got := ExitCode(err); got != CodeUsage {
		t.Fatalf("code = %d, want %d", got, CodeUsage)
	}
	if ExitCode(errors.New("plain")) != CodeError {
		t.Fatal("plain error must map to CodeError")
	}
	if ExitCode(nil) != CodeOK {
		t.Fatal("nil must map to CodeOK")
	}
}

func TestSignalCodes(t *testing.T) {
	if c := ExitCode(Interrupted()); c != 130 {
		t.Errorf("Interrupted: %d", c)
	}
	if c := ExitCode(Terminated()); c != 143 {
		t.Errorf("Terminated: %d", c)
	}
}

func TestAPIUnwrapsClassifiedError(t *testing.T) {
	inner := New(CodeRateLimited, "rate limited by the server until 2026-03-04T13:00:00Z (1h0m0s from now); try again then")
	err := API(nil, &url.Error{Op: "Get", URL: "https://chat.example.com/api/v4/users/me", Err: inner})
	if got := ExitCode(err); got != CodeRateLimited {
		t.Fatalf("code = %d, want %d", got, CodeRateLimited)
	}
	if msg := err.Error(); strings.Contains(msg, `Get "`) || !strings.HasPrefix(msg, "rate limited by the server until") {
		t.Fatalf("message %q", msg)
	}
}
