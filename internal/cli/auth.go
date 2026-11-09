package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/spf13/cobra"

	"github.com/VitalyOstanin/mm-cli/internal/auth"
	"github.com/VitalyOstanin/mm-cli/internal/clierr"
	"github.com/VitalyOstanin/mm-cli/internal/config"
)

const (
	pendingMaxAge = 30 * time.Minute

	// maxTokenBytes bounds the token read from stdin.
	maxTokenBytes = 4096
	// maxCallbackBytes bounds the /login/desktop address read from stdin.
	maxCallbackBytes = 16 << 10
)

// callbackMinutes is the lifetime of the /login/desktop address in minutes,
// as the help and prompts show it.
var callbackMinutes = int(auth.CallbackLifetime / time.Minute)

// ssoMethodList is "saml, gitlab, ..." from auth.Methods.
var ssoMethodList = strings.Join(auth.MethodNames(), ", ")

// ssoMethodChoice is "saml, gitlab, ... or office365" for error messages.
func ssoMethodChoice() string {
	names := auth.MethodNames()
	last := len(names) - 1
	return strings.Join(names[:last], ", ") + " or " + names[last]
}

func newAuthCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Log in, check and end sessions; list, choose and delete profiles",
		Long: `Sign in to a Mattermost server, check the session and end it, as mmctl auth
does. A login creates or updates a profile, a saved server connection with
its own session (mm-cli help config); later commands use it. Several
profiles keep several servers side by side: auth list shows them, auth set
chooses the current one, --profile or $MM_PROFILE picks one per command,
auth delete removes one. Signing in without a terminal: mm-cli help login.`,
	}
	cmd.AddCommand(newLoginCmd(app), newCheckCmd(app), newLogoutCmd(app),
		newAuthListCmd(app), newAuthCurrentCmd(app), newAuthSetCmd(app), newAuthDeleteCmd(app))
	return cmd
}

func newLoginCmd(app *App) *cobra.Command {
	var tokenStdin, noBrowser bool
	var sso, callback string
	cmd := &cobra.Command{
		Use:   "login [<url>]",
		Short: "Log in through the browser (SSO) or with a token from stdin",
		Long: fmt.Sprintf(`Log in through the same browser flow the desktop app uses.

mm-cli opens the sign-in page; after signing in, the browser lands on a
.../login/desktop?... page. Paste that address into the terminal. Without a
terminal, mm-cli prints the sign-in address and exits with code 0 without
logging in (with --json it prints {"status": "pending", ...}); finish the
login within %d minutes with --callback - and the address on stdin.
--callback '<address>' also works, but the address carries a one-time token
and is then visible in the process list and the shell history.

--token-stdin reads a personal access token or a session token from stdin.
Tokens are never accepted as arguments: those show up in the process list.

The profile name defaults to the server host with dots replaced by '-'.
--team sets the profile's default team; an unknown team is exit code 2.

With --json a finished login prints {"status": "logged_in", "profile": ...,
"url": ..., "user": {"id": ..., "username": ...}, "team": ..., "teams": [...]}.
Exit code 2 for a wrong URL, flags or an expired started login; 3 when the
server rejects the sign-in. More: mm-cli help login.`, callbackMinutes),
		Example: `  mm-cli auth login https://chat.example.com
  mm-cli auth login https://chat.example.com --no-browser
  mm-cli auth login --profile chat-example-com --callback - < address.txt
  mm-cli auth login https://chat.example.com --token-stdin < token.txt`,
		Args: argsRange(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.runLogin(cmd.Context(), args, tokenStdin, noBrowser, sso, callback)
		},
	}
	cmd.Flags().BoolVar(&tokenStdin, "token-stdin", false, "read the token from stdin")
	cmd.Flags().StringVar(&sso, "sso", "", "SSO method when the server has several: "+ssoMethodList)
	cmd.Flags().StringVar(&callback, "callback", "", "finish a started login with the /login/desktop address; '-' reads it from stdin (an address given here is visible in the process list)")
	cmd.Flags().BoolVar(&noBrowser, "no-browser", false, "print the sign-in address without opening a browser")
	return cmd
}

// callbackHelp explains where to find the /login/desktop address. A browser
// that already has a Mattermost session leaves that page at once for the
// default channel, so the address is then only in the history.
const callbackHelp = `After signing in, the browser lands on .../login/desktop?client_token=...&server_token=...
Copy that address. Ignore the offer to open the desktop app: it does not complete this login.
If the browser is already signed in to Mattermost, it moves on to a channel at once;
take the address from the browser history (search for "login/desktop"), or sign in
from a private window.

`

func normalizeURL(raw string) (string, error) {
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	if err := config.CheckServerURL(raw); err != nil {
		return "", clierr.Usage("%v", err)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", clierr.Usage("invalid server URL %q", raw)
	}
	return strings.TrimRight(u.Scheme+"://"+u.Host+u.Path, "/"), nil
}

var profileNameJunkRe = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

// profileNameFromURL derives a valid profile name from the host and port:
// every run of other characters becomes one '-', so "[::1]:8065" gives
// "1-8065" and "chat.example.com" gives "chat-example-com".
func profileNameFromURL(u string) string {
	p, err := url.Parse(u)
	if err != nil {
		return ""
	}
	name := p.Hostname()
	if port := p.Port(); port != "" {
		name += "-" + port
	}
	name = strings.Trim(profileNameJunkRe.ReplaceAllString(name, "-"), "-")
	if len(name) > 64 {
		name = strings.TrimRight(name[:64], "-")
	}
	return name
}

func (a *App) runLogin(ctx context.Context, args []string, tokenStdin, noBrowser bool, sso, callback string) error {
	if err := checkLoginFlags(tokenStdin, sso, callback); err != nil {
		return err
	}
	cfg, err := a.loadConfig()
	if err != nil {
		return err
	}
	name, serverURL, err := a.resolveLoginTarget(cfg, args)
	if err != nil {
		return err
	}
	if callback != "" {
		return a.finishSSOFromCallback(ctx, cfg, name, callback)
	}
	if serverURL == "" {
		return clierr.Usage("pass the server URL")
	}
	prof := &config.Profile{URL: serverURL, RateLimit: rateOf(cfg, name)}
	c := a.newProfileClient(name, prof, "")
	if tokenStdin {
		return a.loginWithStdinToken(ctx, c, name, serverURL)
	}
	return a.startSSO(ctx, cfg, c, name, serverURL, sso, noBrowser)
}

// checkLoginFlags rejects flag combinations before anything is read.
func checkLoginFlags(tokenStdin bool, sso, callback string) error {
	if tokenStdin && callback != "" {
		return clierr.Usage("--token-stdin and --callback cannot be used together")
	}
	if sso != "" {
		if _, ok := auth.FindMethod(sso); !ok {
			return clierr.Usage("unknown SSO method %q; expected %s", sso, ssoMethodChoice())
		}
	}
	return nil
}

// resolveLoginTarget picks the profile name and the server URL: the URL
// argument, --profile, the name derived from the URL, or the selected
// profile. The URL may stay empty when the profile does not exist yet.
func (a *App) resolveLoginTarget(cfg *config.Config, args []string) (name, serverURL string, err error) {
	if len(args) == 1 {
		if serverURL, err = normalizeURL(args[0]); err != nil {
			return "", "", err
		}
	}
	name = a.profile
	if name == "" && serverURL != "" {
		name = profileNameFromURL(serverURL)
	}
	if name == "" {
		n, p, err := cfg.Select("", a.Env("MM_PROFILE"))
		if err != nil {
			return "", "", clierr.Usage("pass the server URL: %v", err)
		}
		name, serverURL = n, p.URL
	}
	if err := config.ValidateProfileName(name); err != nil {
		return "", "", clierr.Usage("%v", err)
	}
	if serverURL == "" {
		if p := cfg.Profiles[name]; p != nil {
			serverURL = p.URL
			if err := config.CheckServerURL(serverURL); err != nil {
				return "", "", clierr.Usage("profile %q: %v", name, err)
			}
		}
	}
	return name, serverURL, nil
}

// finishSSOFromCallback completes a started login with the --callback
// address; "-" reads the address from stdin.
func (a *App) finishSSOFromCallback(ctx context.Context, cfg *config.Config, name, callback string) error {
	pend, err := a.Store.LoadPending(name)
	if err != nil {
		return configErr(err)
	}
	if callback == "-" {
		if callback, err = readLineCtx(ctx, io.LimitReader(a.In, maxCallbackBytes)); err != nil && ctx.Err() == nil {
			return clierr.Usage("--callback -: cannot read the address from stdin: %v", err)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if strings.TrimSpace(callback) == "" {
			return clierr.Usage("--callback -: stdin is empty; pass the /login/desktop address on stdin")
		}
	}
	return a.finishSSO(ctx, rateOf(cfg, name), name, pend, callback)
}

// loginWithStdinToken logs in with exactly one token read from stdin.
func (a *App) loginWithStdinToken(ctx context.Context, c *model.Client4, name, serverURL string) error {
	data, err := io.ReadAll(io.LimitReader(a.In, maxTokenBytes))
	if err != nil {
		return err
	}
	tok := strings.TrimSpace(string(data))
	if tok == "" || strings.ContainsAny(tok, " \t\r\n") {
		return clierr.Usage("stdin must contain exactly one token")
	}
	c.SetToken(tok)
	return a.finishLogin(ctx, name, serverURL, "token", c, tok)
}

// startSSO saves a pending login, shows the sign-in address and, on a
// terminal, waits for the pasted /login/desktop address. Without a terminal
// it only tells how to finish with --callback -.
func (a *App) startSSO(ctx context.Context, cfg *config.Config, c *model.Client4, name, serverURL, sso string, noBrowser bool) error {
	ccfg, resp, err := c.GetClientConfig(ctx, "")
	if err != nil {
		return clierr.API(resp, err)
	}
	m, err := auth.Choose(auth.AvailableMethods(ccfg), sso)
	if err != nil {
		return clierr.Usage("%v", err)
	}
	ct, err := auth.NewClientToken()
	if err != nil {
		return err
	}
	pend := config.Pending{URL: serverURL, Method: m.Name, ClientToken: ct, CreatedAt: a.Now().Unix()}
	if err := a.Store.SavePending(name, pend); err != nil {
		return err
	}
	loginURL := auth.LoginURL(serverURL, m, ct)
	fmt.Fprintf(a.Err, "Sign in with %s at:\n\n  %s\n\n", m.Name, loginURL)
	if !noBrowser {
		if err := a.OpenBrowser(loginURL); err != nil {
			fmt.Fprintf(a.Err, "Could not open a browser (%v); open the address above manually.\n", err)
		}
	}
	if !a.IsTerminal() {
		return a.printLoginPending(name, serverURL, m.Name, loginURL)
	}
	return a.waitForPastedCallback(ctx, cfg, name, pend)
}

// printLoginPending tells how to finish the login with --callback - when
// there is no terminal to paste into; with --json it prints the pending
// status. The exit code is 0.
func (a *App) printLoginPending(name, serverURL, method, loginURL string) error {
	next := "mm-cli auth login --profile " + name + " --callback -"
	fmt.Fprint(a.Err, callbackHelp)
	fmt.Fprintf(a.Err, "Then, within %d minutes, run the command below and pass the address on its stdin:\n\n  %s\n\n"+
		"(--callback '<address>' also works, but the address is then visible in the process list and the shell history.)\n", callbackMinutes, next)
	if a.json {
		return a.printJSON(loginPending{
			Status: "pending", Profile: name, URL: serverURL, Method: method,
			LoginURL: loginURL, ExpiresIn: int(auth.CallbackLifetime / time.Second), Next: next,
		})
	}
	return nil
}

// waitForPastedCallback reads the /login/desktop address from the terminal
// and finishes the login.
func (a *App) waitForPastedCallback(ctx context.Context, cfg *config.Config, name string, pend config.Pending) error {
	fmt.Fprint(a.Err, callbackHelp)
	fmt.Fprintf(a.Err, "Paste it here within %d minutes:\n> ", callbackMinutes)
	line, err := readLineCtx(ctx, a.In)
	if ctx.Err() != nil {
		fmt.Fprintln(a.Err)
		return ctx.Err()
	}
	if err != nil && strings.TrimSpace(line) == "" {
		return clierr.Usage("no address pasted; finish later with --callback -")
	}
	return a.finishSSO(ctx, rateOf(cfg, name), name, pend, line)
}

// loginPending is the --json output of a login that waits for --callback.
type loginPending struct {
	Status    string `json:"status"`
	Profile   string `json:"profile"`
	URL       string `json:"url"`
	Method    string `json:"method"`
	LoginURL  string `json:"login_url"`
	ExpiresIn int    `json:"expires_in"`
	Next      string `json:"next"`
}

// rateOf returns the rate limit stored for a profile, if any.
func rateOf(cfg *config.Config, name string) *config.RateLimit {
	if p := cfg.Profiles[name]; p != nil {
		return p.RateLimit
	}
	return nil
}

func (a *App) finishSSO(ctx context.Context, rate *config.RateLimit, name string, pend config.Pending, pasted string) error {
	if a.Now().Sub(time.Unix(pend.CreatedAt, 0)) > pendingMaxAge {
		_ = a.Store.DeletePendingIf(name, pend.ClientToken)
		return clierr.Usage("the started login is too old; run `mm-cli auth login <url>` again")
	}
	st, err := auth.ParseCallback(pend.URL, pasted, pend.ClientToken)
	if err != nil {
		return clierr.Usage("%v", err)
	}
	c := a.newProfileClient(name, &config.Profile{URL: pend.URL, RateLimit: rate}, "")
	_, tok, resp, err := auth.Exchange(ctx, c, st)
	if err != nil {
		return exchangeErr(ctx, resp, err)
	}
	_ = a.Store.DeletePendingIf(name, pend.ClientToken)
	return a.finishLogin(ctx, name, pend.URL, pend.Method, c, tok)
}

// exchangeErr classifies a failed token exchange. Only a refusal of the
// token (400, 401, 403) asks for a new login: an overloaded server (429,
// 5xx) or a network error keeps its own exit code, since the one-time token
// may still be valid, and Ctrl+C returns the context error.
func exchangeErr(ctx context.Context, resp *model.Response, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	status := 0
	if resp != nil {
		status = resp.StatusCode
	}
	var ae *model.AppError
	if status == 0 && errors.As(err, &ae) {
		status = ae.StatusCode
	}
	switch status {
	case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden:
		return clierr.Wrap(clierr.CodeAuth, err, "%v; run `mm-cli auth login <url>` again", err)
	}
	return clierr.API(resp, err)
}

type loginResult struct {
	// Status is "logged_in" in the output of auth login and empty in
	// auth check.
	Status        string      `json:"status,omitempty"`
	Profile       string      `json:"profile"`
	URL           string      `json:"url"`
	User          userSummary `json:"user"`
	Team          string      `json:"team"`
	Teams         []string    `json:"teams,omitempty"`
	ServerVersion string      `json:"server_version,omitempty"`
}

type userSummary struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

// finishLogin checks the token, then stores the session and the profile. The
// configuration is read again right before it is written, and only this
// profile and an empty default are changed, so that changes other processes
// made during the login (up to several minutes in the browser) are kept.
func (a *App) finishLogin(ctx context.Context, name, serverURL, method string, c *model.Client4, tok string) error {
	me, resp, err := c.GetMe(ctx, "")
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusUnauthorized {
			return clierr.Wrap(clierr.CodeAuth, err, "the server rejected the token")
		}
		return clierr.API(resp, err)
	}
	teams, resp, err := c.GetTeamsForUser(ctx, me.Id, "")
	if err != nil {
		return clierr.API(resp, err)
	}
	teamNames := make([]string, len(teams))
	for i, t := range teams {
		teamNames[i] = t.Name
	}
	if a.team != "" && !slices.Contains(teamNames, a.team) {
		return clierr.Usage("team %q not found; your teams: %s", a.team, strings.Join(teamNames, ", "))
	}
	cfg, err := a.loadConfig()
	if err != nil {
		return err
	}
	p := cfg.Profiles[name]
	if p == nil {
		p = &config.Profile{}
		cfg.Profiles[name] = p
	}
	if p.URL != serverURL || p.UserID != me.Id {
		// Cached ids belong to the previous user or server.
		if err := a.Store.DeleteCache(name); err != nil {
			return err
		}
	}
	p.URL, p.UserID, p.Username, p.AuthMethod = serverURL, me.Id, me.Username, method
	switch {
	case a.team != "":
		p.Team = a.team
	case p.Team != "" && !slices.Contains(teamNames, p.Team):
		p.Team = ""
	}
	if p.Team == "" && len(teams) == 1 {
		p.Team = teams[0].Name
	}
	if err := a.Store.SaveSession(name, tok); err != nil {
		return err
	}
	if cfg.Default == "" {
		cfg.Default = name
	}
	if err := a.Store.Save(cfg); err != nil {
		return err
	}
	if a.json {
		return a.printJSON(loginResult{Status: "logged_in", Profile: name, URL: serverURL, User: userSummary{me.Id, me.Username}, Team: p.Team, Teams: teamNames})
	}
	fmt.Fprintf(a.Out, "Logged in to %s as @%s (profile %s", serverURL, me.Username, name)
	if p.Team != "" {
		fmt.Fprintf(a.Out, ", team %s", p.Team)
	}
	fmt.Fprintln(a.Out, ").")
	if p.Team == "" && len(teams) > 1 {
		fmt.Fprintf(a.Out, "Several teams (%s); pass --team to commands, or run `mm-cli auth login --team <name>` to set a default.\n", strings.Join(teamNames, ", "))
	}
	return nil
}

func newCheckCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "check",
		Short: "Show who is logged in and the server version",
		Long: `Show the signed-in user, the server, the profile, the current team and the
server version. Changes nothing; use it to check that a session works.

With --json it prints {"profile": ..., "url": ..., "user": {"id": ...,
"username": ...}, "team": ..., "server_version": ...}. Exit code 3 when nobody
is signed in or the session has expired: run mm-cli auth login.`,
		Example: `  mm-cli auth check
  mm-cli auth check --json
  mm-cli auth check --profile chat-example-com`,
		Args: argsRange(0, 0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := app.openSession()
			if err != nil {
				return err
			}
			defer s.Close()
			me, resp, err := s.Client.GetMe(cmd.Context(), "")
			if err != nil {
				return clierr.API(resp, err)
			}
			if app.json {
				return app.printJSON(loginResult{Profile: s.Name, URL: s.Profile.URL, User: userSummary{me.Id, me.Username}, Team: s.Team, ServerVersion: resp.ServerVersion})
			}
			team := s.Team
			if team == "" {
				team = "(none)"
			}
			fmt.Fprintf(app.Out, "@%s on %s\nprofile: %s\nteam: %s\nserver version: %s\n", me.Username, s.Profile.URL, s.Name, team, resp.ServerVersion)
			return nil
		},
	}
}

func newLogoutCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "End the session on the server and delete the local token",
		Long: `End the session on the server and delete the local session file of the
profile. The profile itself stays (mm-cli auth delete deletes it). When the
server cannot end the session, a warning goes to stderr and the local token
is deleted anyway; a session that had already expired is not an error.

With --json it prints {"action": "logged_out", "profile": "<name>"}.`,
		Example: `  mm-cli auth logout
  mm-cli auth logout --profile chat-example-com --json`,
		Args: argsRange(0, 0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := app.openSession()
			if err != nil {
				return err
			}
			defer s.Close()
			if resp, err := s.Client.Logout(cmd.Context()); err != nil {
				e := clierr.API(resp, err)
				if clierr.ExitCode(e) != clierr.CodeAuth {
					fmt.Fprintf(app.Err, "mm-cli: warning: server logout failed: %v\n", e)
				}
			}
			if err := app.Store.DeleteSession(s.Name); err != nil {
				return configErr(err)
			}
			return app.printDone(actionResult{Action: "logged_out", Profile: s.Name}, "Logged out (profile %s).", s.Name)
		},
	}
}
