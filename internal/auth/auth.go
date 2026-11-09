// Package auth implements the desktop-app SSO login flow: the browser signs
// in and lands on /login/desktop, whose server_token is exchanged for a
// session token.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

// Method is one SSO login method of the server.
type Method struct {
	Name      string
	ConfigKey string
	Path      string
}

// CallbackLifetime is how long the server accepts the server_token of the
// /login/desktop address.
const CallbackLifetime = 3 * time.Minute

// Methods lists the SSO methods the desktop flow supports.
var Methods = []Method{
	{"saml", "EnableSaml", "/login/sso/saml"},
	{"gitlab", "EnableSignUpWithGitLab", "/oauth/gitlab/login"},
	{"openid", "EnableSignUpWithOpenId", "/oauth/openid/login"},
	{"google", "EnableSignUpWithGoogle", "/oauth/google/login"},
	{"office365", "EnableSignUpWithOffice365", "/oauth/office365/login"},
}

// AvailableMethods returns the methods enabled in the client configuration.
func AvailableMethods(cfg map[string]string) []Method {
	var out []Method
	for _, m := range Methods {
		if cfg[m.ConfigKey] == "true" {
			out = append(out, m)
		}
	}
	return out
}

func names(ms []Method) string {
	n := make([]string, len(ms))
	for i, m := range ms {
		n[i] = m.Name
	}
	return strings.Join(n, ", ")
}

// Choose picks the method named by flag, or the only available one.
func Choose(available []Method, flag string) (Method, error) {
	if flag != "" {
		for _, m := range available {
			if m.Name == flag {
				return m, nil
			}
		}
		return Method{}, fmt.Errorf("SSO method %q is not enabled on this server; available: %s", flag, names(available))
	}
	switch len(available) {
	case 0:
		return Method{}, errors.New("the server reports no SSO login methods; use --token-stdin")
	case 1:
		return available[0], nil
	}
	return Method{}, fmt.Errorf("several SSO methods are enabled (%s); choose one with --sso", names(available))
}

// MethodNames returns the names of Methods in order.
func MethodNames() []string {
	names := make([]string, len(Methods))
	for i, m := range Methods {
		names[i] = m.Name
	}
	return names
}

// FindMethod returns a method by name.
func FindMethod(name string) (Method, bool) {
	for _, m := range Methods {
		if m.Name == name {
			return m, true
		}
	}
	return Method{}, false
}

// NewClientToken returns a random token that ties the browser login to this
// login attempt.
func NewClientToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// LoginURL is the page the browser opens to sign in.
func LoginURL(server string, m Method, clientToken string) string {
	return strings.TrimRight(server, "/") + m.Path + "?desktop_token=" + url.QueryEscape(clientToken)
}

// callbackInText finds the /login/desktop address in pasted text that is more
// than a bare URL, such as a "Copy as cURL" command from the browser.
var callbackInText = regexp.MustCompile(`https?://[^\s'"<>]*/login/desktop[^\s'"<>]*`)

// ParseCallback checks the pasted /login/desktop address and returns its
// server_token. The scheme is not compared: the address is only read for its
// tokens, and browser developer tools may show it as http:// after the
// identity provider's redirect. The host must match the server.
func ParseCallback(server, pasted, clientToken string) (string, error) {
	text := strings.TrimSpace(pasted)
	if m := callbackInText.FindString(text); m != "" {
		text = m
	}
	u, err := url.Parse(text)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", errors.New("the pasted text is not a URL")
	}
	s, err := url.Parse(server)
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(u.Host, s.Host) {
		return "", fmt.Errorf("the pasted URL is for %s, expected %s", u.Host, s.Host)
	}
	if !strings.HasSuffix(strings.TrimRight(u.Path, "/"), "/login/desktop") {
		return "", errors.New("the pasted URL is not the /login/desktop page the browser lands on after signing in")
	}
	q := u.Query()
	if subtle.ConstantTimeCompare([]byte(q.Get("client_token")), []byte(clientToken)) != 1 {
		return "", errors.New("client_token does not match this login attempt; start `mm-cli auth login` again")
	}
	st := q.Get("server_token")
	if st == "" {
		return "", errors.New("server_token is missing from the pasted URL")
	}
	return st, nil
}

// Exchange trades the server_token for a session token. The response is
// returned with an error too, so that the caller can tell a rejected token
// from an overloaded server.
func Exchange(ctx context.Context, c *model.Client4, serverToken string) (*model.User, string, *model.Response, error) {
	user, resp, err := c.LoginWithDesktopToken(ctx, serverToken, "")
	if err != nil {
		return nil, "", resp, fmt.Errorf("login failed (the token is valid for %d minutes): %w", int(CallbackLifetime/time.Minute), err)
	}
	if c.AuthToken == "" {
		return nil, "", resp, errors.New("the server returned no session token")
	}
	return user, c.AuthToken, resp, nil
}

// OpenBrowser opens the URL in the default browser.
func OpenBrowser(u string) error {
	// The URL is built by mm-cli from the profile's server address, and each
	// opener receives it as a single argument, not through a shell.
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.CommandContext(context.Background(), "open", u) //nolint:gosec // see above
	case "windows":
		cmd = exec.CommandContext(context.Background(), "rundll32", "url.dll,FileProtocolHandler", u) //nolint:gosec // see above
	default:
		cmd = exec.CommandContext(context.Background(), "xdg-open", u) //nolint:gosec // see above
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
