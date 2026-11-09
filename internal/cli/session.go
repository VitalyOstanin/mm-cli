package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"

	"github.com/VitalyOstanin/mm-cli/internal/client"
	"github.com/VitalyOstanin/mm-cli/internal/clierr"
	"github.com/VitalyOstanin/mm-cli/internal/config"
	"github.com/VitalyOstanin/mm-cli/internal/render"
	"github.com/VitalyOstanin/mm-cli/internal/resolve"
)

// Session is an authenticated client for the selected server.
type Session struct {
	Name      string
	Server    *config.Server
	Client    *model.Client4
	Resolver  *resolve.Resolver
	Team      string
	cachePath string
	app       *App
	// warnedNames is set once the user-name lookup failure has been reported.
	warnedNames bool
}

// newServerClient returns a client for server p whose rate-limit state is
// shared with other runs for the same server through a file in its cache
// directory, so that a series of short commands keeps honouring the server's
// 429 answers. An empty name keeps the state in memory only.
func (a *App) newServerClient(name string, p *config.Server, token string) *model.Client4 {
	r := a.DefaultRate
	if p.RateLimit != nil && p.RateLimit.PerSecond > 0 {
		r = *p.RateLimit
	}
	o := client.Options{
		URL: p.URL, Token: token,
		PerSecond: r.PerSecond, Burst: r.Burst,
		UserAgent: "mm-cli/" + a.Version,
		Notify:    a.Err,
		Now:       a.Now,
	}
	if name != "" && config.ValidateServerName(name) == nil {
		o.StatePath = filepath.Join(filepath.Dir(a.Store.CachePath(name)), client.StateFile)
	}
	return client.New(o)
}

// configErr classifies an error of the on-disk configuration: a missing
// server or session is an authentication problem, a config.yaml that cannot
// be used as written or a server choice the command line must fix is a usage
// error, and anything else (a file that cannot be read, a damaged session or
// pending-login file) is a general error.
func configErr(err error) error {
	if err == nil {
		return nil
	}
	var ce *clierr.Error
	if errors.As(err, &ce) {
		return err
	}
	switch {
	case errors.Is(err, config.ErrNoServer), errors.Is(err, config.ErrNoSession):
		return clierr.Wrap(clierr.CodeAuth, err, "%v", err)
	case errors.Is(err, config.ErrInvalidConfig), errors.Is(err, config.ErrServerSelection),
		errors.Is(err, config.ErrNoPending):
		return clierr.Wrap(clierr.CodeUsage, err, "%v", err)
	}
	return clierr.Wrap(clierr.CodeError, err, "%v", err)
}

func (a *App) openSession() (*Session, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return nil, err
	}
	name, p, err := cfg.Select(a.server, a.Env("MM_SERVER"))
	if err != nil {
		return nil, configErr(err)
	}
	tok, err := a.Store.LoadSession(name)
	if err != nil {
		if errors.Is(err, config.ErrNoSession) {
			return nil, clierr.Wrap(clierr.CodeAuth, err, "not logged in to server %q; run `mm-cli auth login`", name)
		}
		return nil, configErr(err)
	}
	team := a.team
	if team == "" {
		team = p.Team
	}
	c := a.newServerClient(name, p, tok)
	path := a.Store.CachePath(name)
	cache, err := resolve.ReadCache(path)
	if err != nil {
		fmt.Fprintf(a.Err, "mm-cli: warning: cannot read the cache, starting empty: %v\n", err)
	}
	cache.Now = func() time.Time { return a.Now() }
	return &Session{
		Name: name, Server: p, Client: c, Team: team,
		Resolver:  &resolve.Resolver{API: c, Cache: cache, MeID: p.UserID, Team: team},
		cachePath: path, app: a,
	}, nil
}

// Close saves the address cache; a failure is only a warning.
func (s *Session) Close() {
	if err := s.Resolver.Cache.Save(s.cachePath); err != nil {
		fmt.Fprintf(s.app.Err, "mm-cli: warning: cannot save the cache: %v\n", err)
	}
}

// withChannel resolves the address and runs fn. When fn fails with "not
// found" for a cached id, one request tells whether the channel itself is
// gone; only then is the cache entry dropped and fn run once more. A 404 about
// something else (a missing root post, bookmark, ...) is returned as is, so fn
// never repeats its changes.
func (s *Session) withChannel(ctx context.Context, raw string, fn func(channelID string) error) error {
	addr, err := resolve.Parse(raw)
	if err != nil {
		return err
	}
	res, err := s.Resolver.Resolve(ctx, addr)
	if err != nil {
		return err
	}
	err = fn(res.ChannelID)
	if !res.Cached || clierr.ExitCode(err) != clierr.CodeNotFound {
		return err
	}
	if gone, cerr := s.channelGone(ctx, res.ChannelID); cerr != nil || !gone {
		return err
	}
	s.Resolver.Forget(res)
	res, err = s.Resolver.Resolve(ctx, addr)
	if err != nil {
		return err
	}
	return fn(res.ChannelID)
}

// channelGone asks the server, with one request, whether a channel id no
// longer leads to a usable channel: missing, not accessible or archived.
func (s *Session) channelGone(ctx context.Context, id string) (bool, error) {
	ch, resp, err := s.Client.GetChannel(ctx, id)
	if err != nil {
		if resp != nil && (resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusForbidden) {
			return true, nil
		}
		return false, clierr.API(resp, err)
	}
	return ch.DeleteAt != 0, nil
}

// checkedChannel resolves an address for a long-running command such as
// watch, where a stale id would fail silently. A cached id or an id given
// directly is checked with one request; a stale cached id is resolved again.
func (s *Session) checkedChannel(ctx context.Context, raw string) (string, error) {
	addr, err := resolve.Parse(raw)
	if err != nil {
		return "", err
	}
	res, err := s.Resolver.Resolve(ctx, addr)
	if err != nil {
		return "", err
	}
	if !res.Cached && addr.Kind != resolve.KindID {
		return res.ChannelID, nil
	}
	gone, err := s.channelGone(ctx, res.ChannelID)
	if err != nil {
		return "", err
	}
	if !gone {
		return res.ChannelID, nil
	}
	if !res.Cached {
		return "", clierr.New(clierr.CodeNotFound, "channel %s not found, not accessible or archived", res.ChannelID)
	}
	s.Resolver.Forget(res)
	res, err = s.Resolver.Resolve(ctx, addr)
	if err != nil {
		return "", err
	}
	return res.ChannelID, nil
}

// usernames returns names for the user ids, asking the server once for the
// ones not cached. Failure leaves names out rather than failing the command.
func (s *Session) usernames(ctx context.Context, ids []string) map[string]string {
	out := map[string]string{}
	var missing []string
	seen := map[string]bool{}
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		n, ok := s.Resolver.Cache.Name(id)
		if ok {
			// A stale name is still shown if the server cannot be asked.
			out[id] = n
		}
		if !ok || s.Resolver.Cache.NameStale(id) {
			missing = append(missing, id)
		}
	}
	if len(missing) == 0 {
		return out
	}
	users, _, err := s.Client.GetUsersByIds(ctx, missing)
	if err != nil {
		if ctx.Err() == nil && !s.warnedNames {
			s.warnedNames = true
			fmt.Fprintf(s.app.Err, "mm-cli: warning: cannot look up user names, showing ids: %v\n", err)
		}
		return out
	}
	for _, u := range users {
		out[u.Id] = u.Username
		s.Resolver.Cache.PutUser(u.Id, u.Username)
	}
	return out
}

// confirm asks on the terminal unless yes is set; without a terminal it
// refuses. Canceling ctx (Ctrl+C) stops the wait and returns ctx.Err().
func (a *App) confirm(ctx context.Context, prompt string, yes bool) error {
	if yes {
		return nil
	}
	if !a.IsTerminal() {
		return clierr.Usage("refusing to continue without --confirm when stdin is not a terminal")
	}
	fmt.Fprintf(a.Err, "%s [y/N] ", prompt)
	line, err := readLineCtx(ctx, a.In)
	if ctx.Err() != nil {
		fmt.Fprintln(a.Err)
		return ctx.Err()
	}
	if err != nil {
		line = ""
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return nil
	}
	return clierr.New(clierr.CodeError, "canceled")
}

// readLineCtx reads one line from r without the line ending; a last line
// without a newline counts as a line. The read runs in a goroutine, so that
// canceling ctx returns ctx.Err() at once instead of waiting for input that
// may never come: the signal handler only cancels ctx, so a blocked read
// would otherwise keep the process alive after Ctrl+C or SIGTERM. The
// goroutine stays blocked until input arrives or the process exits.
func readLineCtx(ctx context.Context, r io.Reader) (string, error) {
	type result struct {
		line string
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		line, err := bufio.NewReader(r).ReadString('\n')
		if errors.Is(err, io.EOF) && line != "" {
			err = nil
		}
		ch <- result{strings.TrimRight(line, "\r\n"), err}
	}()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case res := <-ch:
		return res.line, res.err
	}
}

func validID(id, what string) error {
	if !model.IsValidId(id) {
		return clierr.Usage("invalid %s %q: expected a 26-character id", what, id)
	}
	return nil
}

// printPosts prints posts (oldest first) as text or JSON, resolving author
// names with at most one request.
func (s *Session) printPosts(ctx context.Context, posts []*model.Post, truncate int) error {
	ids := make([]string, len(posts))
	for i, p := range posts {
		ids[i] = p.UserId
	}
	names := s.usernames(ctx, ids)
	out := make([]render.Post, len(posts))
	for i, p := range posts {
		out[i] = render.FromModel(p, names)
	}
	if s.app.json {
		return s.app.printJSON(out)
	}
	return render.PostsText(s.app.Out, out, s.app.Location, truncate)
}
