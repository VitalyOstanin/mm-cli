package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"

	"github.com/VitalyOstanin/mm-cli/internal/fakemm"
)

func TestAvailableMethods(t *testing.T) {
	got := AvailableMethods(map[string]string{"EnableSaml": "true", "EnableSignUpWithGitLab": "false", "EnableSignUpWithOpenId": "true"})
	if len(got) != 2 || got[0].Name != "saml" || got[1].Name != "openid" {
		t.Fatalf("got %+v", got)
	}
}

func TestChoose(t *testing.T) {
	saml, _ := FindMethod("saml")
	gitlab, _ := FindMethod("gitlab")
	if m, err := Choose([]Method{saml}, ""); err != nil || m.Name != "saml" {
		t.Fatalf("single: %v %v", m, err)
	}
	if _, err := Choose([]Method{saml, gitlab}, ""); err == nil || !strings.Contains(err.Error(), "saml, gitlab") {
		t.Fatalf("several: %v", err)
	}
	if m, err := Choose([]Method{saml, gitlab}, "gitlab"); err != nil || m.Name != "gitlab" {
		t.Fatalf("flag: %v %v", m, err)
	}
	if _, err := Choose([]Method{saml}, "google"); err == nil {
		t.Fatal("disabled method accepted")
	}
	if _, err := Choose(nil, ""); err == nil || !strings.Contains(err.Error(), "--token-stdin") {
		t.Fatalf("none: %v", err)
	}
}

func TestLoginURL(t *testing.T) {
	saml, _ := FindMethod("saml")
	got := LoginURL("https://chat.example.com/", saml, "a b")
	if got != "https://chat.example.com/login/sso/saml?desktop_token=a+b" {
		t.Fatal(got)
	}
	gitlab, _ := FindMethod("gitlab")
	if got := LoginURL("https://chat.example.com", gitlab, "ct"); got != "https://chat.example.com/oauth/gitlab/login?desktop_token=ct" {
		t.Fatal(got)
	}
}

func TestNewClientToken(t *testing.T) {
	a, err := NewClientToken()
	// "dev-" makes the server add isDesktopDev, so that the page opens
	// mattermost-dev:// links.
	if err != nil || len(a) != 64 || !strings.HasPrefix(a, "dev-") {
		t.Fatalf("%q %v", a, err)
	}
	b, _ := NewClientToken()
	if a == b {
		t.Fatal("tokens repeat")
	}
}

func TestParseCallback(t *testing.T) {
	const srv = "https://chat.example.com"
	tests := []struct {
		name, pasted, want, err string
	}{
		{"ok", " https://chat.example.com/login/desktop?client_token=ct&server_token=st \n", "st", ""},
		{"subpath", "https://chat.example.com/login/desktop/?client_token=ct&server_token=st", "st", ""},
		{"other host", "https://evil.example.net/login/desktop?client_token=ct&server_token=st", "", "expected chat.example.com"},
		{"http from devtools", "http://chat.example.com/login/desktop?client_token=ct&server_token=st", "st", ""},
		{"copy as curl", "curl 'http://chat.example.com/login/desktop?client_token=ct&server_token=st' \\\n  -H 'DNT: 1'", "st", ""},
		{"curl to other host", "curl 'https://evil.example.net/login/desktop?client_token=ct&server_token=st'", "", "expected chat.example.com"},
		{"wrong page", "https://chat.example.com/login?client_token=ct&server_token=st", "", "/login/desktop"},
		{"wrong client token", "https://chat.example.com/login/desktop?client_token=xx&server_token=st", "", "does not match"},
		{"no server token", "https://chat.example.com/login/desktop?client_token=ct", "", "missing"},
		{"garbage", "hello", "", "not a URL"},
		{"handler link", "mattermost-dev://chat.example.com/login/desktop?client_token=ct&server_token=st&isDesktopDev=true", "st", ""},
		{"handler link, other host", "mattermost-dev://evil.example.net/login/desktop?client_token=ct&server_token=st", "", "expected chat.example.com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseCallback(srv, tt.pasted, "ct")
			if tt.err != "" {
				if err == nil || !strings.Contains(err.Error(), tt.err) {
					t.Fatalf("err = %v, want %q", err, tt.err)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("got %q %v", got, err)
			}
		})
	}
}

func TestCallbackClientToken(t *testing.T) {
	ct, err := CallbackClientToken("mattermost-dev://chat.example.com/login/desktop?client_token=dev-1&server_token=st")
	if err != nil || ct != "dev-1" {
		t.Fatalf("%q %v", ct, err)
	}
	for _, bad := range []string{"hello", "https://chat.example.com/login/desktop?server_token=st"} {
		if _, err := CallbackClientToken(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestExchange(t *testing.T) {
	s := fakemm.New(t)
	s.Handle(http.MethodPost, "/api/v4/users/login/desktop_token", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["token"] != "st" {
			fakemm.AppError(w, http.StatusUnauthorized, "bad token")
			return
		}
		w.Header().Set("Token", "session-token")
		fakemm.WriteJSON(w, http.StatusOK, &model.User{Id: fakemm.ID("alice"), Username: "alice"})
	})
	c := model.NewAPIv4Client(s.URL)
	u, tok, resp, err := Exchange(context.Background(), c, "st")
	if err != nil || tok != "session-token" || u.Username != "alice" || resp == nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("%v %q %v %v", u, tok, resp, err)
	}
	_, _, resp, err = Exchange(context.Background(), model.NewAPIv4Client(s.URL), "expired")
	if err == nil {
		t.Fatal("expired token accepted")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("the response must be returned for classification: %v", resp)
	}
}
