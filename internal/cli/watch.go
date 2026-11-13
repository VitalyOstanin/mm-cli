package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/spf13/cobra"

	"github.com/VitalyOstanin/mm-cli/internal/client"
	"github.com/VitalyOstanin/mm-cli/internal/clierr"
	"github.com/VitalyOstanin/mm-cli/internal/render"
)

const (
	watchMinBackoff = time.Second
	watchMaxBackoff = time.Minute
	// watchHandshakeTimeout bounds the WebSocket opening handshake.
	watchHandshakeTimeout = 30 * time.Second
	// watchStable is how long a connection must live before the reconnect
	// pause starts again from watchMinBackoff.
	watchStable = time.Minute
	// watchPingSeconds is the server ping interval, as in
	// model.WebSocketClient.
	watchPingSeconds = 60
	// watchPingTimeout is how long a connection may stay without a ping
	// from the server, as in model.WebSocketClient.
	watchPingTimeout = (watchPingSeconds + model.PingTimeoutBufferSeconds) * time.Second
	// watchSeenSize is how many printed messages, and how many printed
	// reactions, watch remembers to avoid printing one twice.
	watchSeenSize = 5000
)

// errWatchAuth means the server rejected the session on the WebSocket.
var errWatchAuth = errors.New("the server rejected the session")

// watchOutputError is a failure to print a message. Unlike a dropped
// connection it does not go away by reconnecting, so watching stops.
type watchOutputError struct{ err error }

func (e *watchOutputError) Error() string { return e.err.Error() }
func (e *watchOutputError) Unwrap() error { return e.err }

// watchedPost is one line of `watch --json` output.
type watchedPost struct {
	Type string `json:"type"`
	render.Post
	// Channel is the address of the channel, ChannelDisplayName the name the
	// app shows; Team is empty for direct and group channels (ADR 0028).
	Channel            string `json:"channel"`
	ChannelDisplayName string `json:"channel_display_name"`
	Team               string `json:"team"`
}

// watchedReaction is a reaction line of `watch --json`.
type watchedReaction struct {
	Type string `json:"type"`
	render.Reaction
	// Team shadows the embedded one, which carries the link team and so is
	// never empty for direct and group channels.
	Team string `json:"team"`
}

func newWatchCmd(app *App) *cobra.Command {
	var truncate int
	var onlyMessages, onlyReactions, notify, includeOwn bool
	var urgency string
	cmd := &cobra.Command{
		Use:         "watch [<channel>...]",
		Annotations: effect(effectRead),
		Short:       "Print new messages and reactions as they arrive",
		Args:        argsChannels(0, unlimited),
		Long: fmt.Sprintf(`Print new messages as they arrive, until interrupted. Without arguments, all
channels you are a member of are watched.

Three modes: without flags, or with --messages alone, new messages;
--reactions alone, only the reactions other people put on your messages;
--messages --reactions, both in one stream. Channel arguments filter both
kinds. Reactions are those added to your messages by others, in channels you
are a member of (the server sends reaction events only for those); your own
reactions are ignored. The event does not say who wrote the message, so the
first reaction on a message costs one GET /posts/<id> request; the answer is
remembered (the last 2000 messages, and your own messages seen while
watching), so more reactions on the same message cost nothing. A message that
cannot be read (403, 404) is skipped silently.

Watching keeps one WebSocket connection open; there is no polling. After the
connection drops it is opened again, waiting 1, 2, 4 ... up to %d seconds, or
as long as the server asks when it answers 429 Too Many Requests.
Events sent while the connection was down are fetched after each
reconnect, once it is authenticated (not after the first connection). The
catch-up starts 60 seconds before the last frame (message, event or ping)
seen on the dropped connection, to allow for clock skew, and prints what it
finds in time order, messages and reactions together, through the same
output as live events; nothing is printed twice, as the last 5000 messages
and 5000 reactions printed are remembered. It costs: one request for your
teams and one for your channels, then one request for each watched channel
with a message newer than that time (at most 200 messages per channel, with
a warning on stderr); with reactions, one search for your messages of the
last 7 days (100 per page, 10 pages at most), at most once per 10 minutes:
after a sooner reconnect this search is postponed with its starting point
kept, so nothing is lost, and it runs at the latest when the 10 minutes
pass; messages are caught up on every reconnect, live reactions are not
delayed. A reaction made while disconnected on a message older than 7 days
is not caught up. A failed request (a lost network, 403 on a channel) is
reported on stderr and skipped, watching goes on, and the missed part is
tried again after the next reconnect. After a catch-up that printed
anything, stderr gets "mm-cli: reconnected; caught up N messages and M
reactions since HH:MM:SS". With --notify, more than 3 notifications of one
catch-up become a single one, "mm-cli: N missed messages, M missed
reactions", with no link.

Watching does not mark channels as read. In text, a reaction is
"[channel] " and the block of reactions (time, @user, :emoji:, the
message, the mattermost:// link). A standard emoji is shown as its
character followed by its name ("👍 :+1:"), a custom one as :name:.

With --json, each event is one JSON object per line (mm-cli help output)
with a "type" field: "post" for a message, with the extra fields "channel",
"channel_display_name" and "team", or "reaction": {"type": "reaction",
"post_id": ..., "root_id": the thread root ("" for a root post),
"channel_id": ..., "channel": ..., "channel_display_name": ..., "team": ...,
"user_id": ..., "user": who reacted, "emoji": the name without colons,
"create_at": ..., "message": the whole text of your message, "permalink":
..., "app_link": mattermost://<server>/<team>/pl/<id>}. In both, "channel" is
the channel address as mm-cli accepts it (the name of a public or private
channel, @<username> for a direct channel, the id for a group message),
"channel_display_name" the name the app shows, "team" the team name, empty for
direct and group channels. To see the context of
a reaction, load the thread with mm-cli post thread <post_id> (any id of the
thread works). Reconnect notices
go to stderr.

--notify also shows a desktop notification for everything watched, besides
the output. Its title is "<user> reacted <emoji>" or "<sender> in
<channel>", its body the start of the message. A standard emoji in the title
is the Unicode character; a custom emoji is ":name:" with its picture
attached (looked up once per emoji and kept in the user cache directory,
mm-cli/emoji; without the picture when that fails). Linux and other unix:
the desktop notification service over D-Bus (any freedesktop notification
server: GNOME, KDE, dunst, mako ...); a click on the notification runs
xdg-open on the app link, for as long as watch runs. macOS: terminal-notifier when installed, a click
opens the message; else osascript, without opening (a hint goes to stderr).
Windows: a toast through powershell.exe; a click opens the message. Your
own messages give no notification, unless --include-own is given.

--urgency low|normal|critical (default normal) sets the freedesktop urgency
of every notification of the run: messages, reactions, the catch-up summary
and the sign-in notice. It needs --notify. On GNOME, critical keeps the
banner on screen until it is closed and shows it in Do Not Disturb mode too.
It has no effect on macOS and Windows.

--include-own is for checking notifications: your own reactions on your
messages are shown and announced too, and your own new messages give a
notification. Reactions on other people's messages stay ignored. It applies
to the live stream and to the catch-up. To try it alone, post to @me, react
to the message and click the notification.

With --notify, a missing or expired session (not logged in at the start, or
rejected by the server later) gives one notification, "mm-cli: sign in
again", with no button, and exit code 3. A service should not restart on
code 3 (systemd: RestartPreventExitStatus=3). Other errors, such as a lost
network or a 429, only reconnect, with no notification.

Running as a service. Linux: a systemd user unit,
docs/examples/mm-cli-watch.service in the repository (guide:
docs/watch-in-background.md). Install it from the repository root with
mkdir -p ~/.config/systemd/user, then
cp docs/examples/mm-cli-watch.service ~/.config/systemd/user/, set
ExecStart= in the copy to the output of command -v mm-cli (systemd needs
an absolute path), then systemctl --user daemon-reload and
systemctl --user enable --now mm-cli-watch.service; the logs are in
journalctl --user -u mm-cli-watch. The unit has RestartPreventExitStatus=3:
after an expired session it gives the one notification and stays stopped,
instead of a restart loop; run mm-cli auth login, then
systemctl --user restart mm-cli-watch.service. It has
SuccessExitStatus=143, so a stop or a logout (SIGTERM) is not a failure.
The token is read from the OS secret store, so the unit belongs to the
graphical session (PartOf= and WantedBy=graphical-session.target). In a
container or on a headless host add Environment=MM_TOKEN_STORAGE=file to
the unit. macOS: a launchd agent; Windows: a Task Scheduler task at logon;
both run mm-cli watch --reactions --notify at login.

Stopped by SIGINT or SIGTERM, it exits with code 130 or 143. Exit code 2
for an invalid --urgency value or --urgency without --notify. Exit code 1
when --notify finds no notification service on the session bus (or
powershell.exe on Windows), 5 for an unknown channel in the arguments.

`, int(watchMaxBackoff/time.Second)) + channelHelp,
		Example: `  mm-cli watch
  mm-cli watch town-square @bob
  mm-cli watch --json @me
  mm-cli watch --reactions --notify
  mm-cli watch --reactions --notify --include-own @me
  mm-cli watch --reactions --notify --urgency critical
  mm-cli watch --messages --reactions --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			level, err := parseUrgency(urgency, cmd.Flags().Changed("urgency"), notify)
			if err != nil {
				return err
			}
			// Fail before connecting when the notifier tool is missing.
			// alert shows the one notice that ends the run (sign in again); it
			// returns only after the notification is up.
			var notifier, alert func(context.Context, Notification)
			if notify {
				notifier, alert = app.Notify, app.Notify
				if notifier == nil {
					d, err := newDesktopNotifier(app, level)
					if err != nil {
						return err
					}
					defer d.Close()
					notifier, alert = d.Notify, d.NotifyNow
				}
			}
			s, err := app.openSession()
			if err != nil {
				if alert != nil && clierr.ExitCode(err) == clierr.CodeAuth {
					alert(ctx, signInNotice(app.selectedServerName()))
				}
				return err
			}
			defer s.Close()
			filter := map[string]bool{}
			for _, a := range args {
				id, err := s.checkedChannel(ctx, a)
				if err != nil {
					return err
				}
				filter[id] = true
			}
			w := &watcher{
				s: s, filter: filter, truncate: truncate, notify: notifier, includeOwn: includeOwn,
				messages: onlyMessages || !onlyReactions, reactions: onlyReactions,
				emoji: newEmojiImages(s), posts: newPostCache(watchPostCache), chans: map[string]watchChannel{}, teams: map[string]string{},
				seenPosts: newSeenSet(watchSeenSize), seenReactions: newSeenSet(watchSeenSize),
			}
			err = s.watch(ctx, w)
			if alert != nil && errors.Is(err, errWatchAuth) {
				alert(ctx, signInNotice(quoteServerName(s.Name)))
			}
			return err
		},
	}
	cmd.Flags().BoolVar(&onlyMessages, "messages", false, "watch new messages (the default; with --reactions, both)")
	cmd.Flags().BoolVar(&onlyReactions, "reactions", false, "watch reactions other people put on your messages")
	cmd.Flags().BoolVar(&notify, "notify", false, "also show a desktop notification for each event")
	cmd.Flags().StringVar(&urgency, "urgency", "normal", "urgency of the notifications: low, normal or critical (Linux only; needs --notify)")
	cmd.Flags().BoolVar(&includeOwn, "include-own", false, "also show and notify about your own reactions and messages (to check notifications)")
	addTruncateFlag(cmd, &truncate)
	return cmd
}

// signInNotice is the notification for a missing or expired session. It has
// no link: there is nothing to open, the user has to run a command.
func signInNotice(server string) Notification {
	return Notification{
		Title: "mm-cli: sign in again",
		Body:  "The session for " + server + " is missing or expired. Run: mm-cli auth login",
	}
}

// selectedServerName is the quoted name of the server the command would use,
// for a notice written when no session could be opened; "the selected
// server" (unquoted) when even that is unknown.
func (a *App) selectedServerName() string {
	cfg, err := a.loadConfig()
	if err != nil {
		return "the selected server"
	}
	name, _, err := cfg.Select(a.server, a.Env("MM_SERVER"))
	if err != nil || name == "" {
		return "the selected server"
	}
	return quoteServerName(name)
}

// quoteServerName is how a notice writes a server name; both places that
// build a sign-in notice go through it, so the text does not depend on
// whether the session could be opened.
func quoteServerName(name string) string {
	return fmt.Sprintf("%q", name)
}

// watch reads `posted` and `reaction_added` events (the latter only with
// --reactions) until ctx is cancelled, reconnecting with a
// growing pause. Every connection goes through the rate limiter of the
// server, and a ban announced by the server (a 429, here or in another run)
// is waited out before connecting again.
func (s *Session) watch(ctx context.Context, w *watcher) error {
	tr, _ := s.Client.HTTPClient.Transport.(*client.Transport)
	backoff := watchMinBackoff
	for {
		if err := s.waitForServer(ctx, tr); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		started := s.app.Now()
		err := s.watchOnce(ctx, tr, w)
		if ctx.Err() != nil {
			return nil
		}
		if errors.Is(err, errWatchAuth) {
			return clierr.Wrap(clierr.CodeAuth, err, "session expired or invalid; run `mm-cli auth login`")
		}
		var outErr *watchOutputError
		if errors.As(err, &outErr) {
			// The reader went away (`mm-cli watch | head -1`): stop quietly,
			// as other filters in a pipeline do.
			if errors.Is(err, syscall.EPIPE) {
				return nil
			}
			return clierr.Wrap(clierr.CodeError, err, "cannot write output: %v", err)
		}
		if s.app.Now().Sub(started) >= watchStable {
			backoff = watchMinBackoff
		}
		pause := backoff
		if d := banLeft(tr, s.app.Now()); d > pause {
			pause = d
		}
		fmt.Fprintf(s.app.Err, "mm-cli: connection lost (%v); reconnecting in %s\n", err, pause.Round(time.Second))
		if err := s.app.sleep(ctx, pause); err != nil {
			return nil
		}
		backoff = min(backoff*2, watchMaxBackoff)
	}
}

// banLeft is how long the server's ban still holds; zero without one.
func banLeft(tr *client.Transport, now time.Time) time.Duration {
	if tr == nil {
		return 0
	}
	return max(tr.BannedUntil().Sub(now), 0)
}

// waitForServer waits out a ban announced by the server, however long (watch
// runs until interrupted anyway), and then takes a turn of the rate limiter
// shared with the REST requests for the same server.
func (s *Session) waitForServer(ctx context.Context, tr *client.Transport) error {
	if tr == nil {
		return nil
	}
	if d := banLeft(tr, s.app.Now()); d > 0 {
		fmt.Fprintf(s.app.Err, "mm-cli: rate limited by the server, waiting %s\n", d.Round(time.Second))
		if err := s.app.sleep(ctx, d); err != nil {
			return err
		}
	}
	return tr.Acquire(ctx)
}

func websocketURL(server string) string {
	u := strings.TrimRight(server, "/")
	switch {
	case strings.HasPrefix(u, "https://"):
		return "wss://" + strings.TrimPrefix(u, "https://")
	case strings.HasPrefix(u, "http://"):
		return "ws://" + strings.TrimPrefix(u, "http://")
	}
	return u
}

// dialWebSocket opens the connection and sends the authentication challenge.
// The handshake is made here rather than by model.WebSocketClient, which
// drops the server's answer: a 429 is recorded in the shared rate-limit
// state with its Retry-After, and a 401 means the session is invalid.
func (s *Session) dialWebSocket(ctx context.Context, tr *client.Transport) (*websocket.Conn, error) {
	dialer := &websocket.Dialer{Proxy: http.ProxyFromEnvironment, HandshakeTimeout: watchHandshakeTimeout}
	conn, resp, err := dialer.DialContext(ctx, websocketURL(s.Server.URL)+model.APIURLSuffix+"/websocket",
		http.Header{"User-Agent": {s.app.userAgent(s.Server)}})
	if resp != nil && resp.Body != nil {
		defer func() { _ = resp.Body.Close() }()
	}
	if err != nil {
		if resp != nil {
			switch resp.StatusCode {
			case http.StatusTooManyRequests:
				if tr != nil {
					tr.Throttled(resp.Header)
				}
				return nil, fmt.Errorf("%w: %s", err, resp.Status)
			case http.StatusUnauthorized:
				return nil, fmt.Errorf("%w: %w", errWatchAuth, err)
			}
			return nil, fmt.Errorf("%w: %s", err, resp.Status)
		}
		return nil, err
	}
	err = conn.WriteJSON(&model.WebSocketRequest{
		Seq:    1,
		Action: string(model.WebsocketAuthenticationChallenge),
		Data:   map[string]any{"token": s.Client.AuthToken},
	})
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

// watchOnce runs one connection until it drops or ctx is cancelled.
func (s *Session) watchOnce(ctx context.Context, tr *client.Transport, w *watcher) error {
	// The time of the last frame of the previous connection: what happened
	// after it, and before this connection is authenticated, is fetched.
	prevAlive := w.alive
	conn, err := s.dialWebSocket(ctx, tr)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	// The connection is read by a goroutine of its own, so that the server's
	// pings are answered and the read deadline is extended while the main loop
	// is busy: a catch-up after a long outage makes one request per second and
	// would otherwise outlast the ping timeout and drop the connection again.
	// Frames wait in a queue and are handled in order afterwards, so the output
	// of the catch-up still comes before the live events that arrived during it.
	// Everything that touches the watcher stays in this goroutine; the reader
	// shares only the queue and the atomic time of the last ping.
	q := newFrameQueue()
	// A catch-up of reactions skipped by the limit on searches runs when the
	// limit expires, even if no reconnect follows: the timer only wakes this
	// loop, which does the work like for any frame.
	rctx, rcancel := context.WithCancel(ctx)
	defer rcancel()
	armRetry := func() {
		d, ok := w.reactionRetryIn()
		if ok {
			go func() {
				if s.app.sleep(rctx, d) == nil && rctx.Err() == nil {
					q.tick()
				}
			}()
		}
	}
	var pingAt atomic.Int64
	_ = conn.SetReadDeadline(time.Now().Add(watchPingTimeout))
	conn.SetPingHandler(func(data string) error {
		pingAt.Store(s.app.Now().UnixNano())
		_ = conn.SetReadDeadline(time.Now().Add(watchPingTimeout))
		err := conn.WriteControl(websocket.PongMessage, []byte(data), time.Now().Add(time.Second))
		var ne net.Error
		if errors.Is(err, websocket.ErrCloseSent) || errors.As(err, &ne) && ne.Timeout() {
			return nil
		}
		return err
	})
	go func() {
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				q.fail(err)
				return
			}
			q.push(queuedFrame{data: data, at: s.app.Now()})
		}
	}()
	// Pings count as signs of life too; the reader may not touch w.alive.
	defer func() {
		if t := pingAt.Load(); t > 0 {
			if at := time.Unix(0, t); at.After(w.alive) {
				w.alive = at
			}
		}
	}()
	// A server drops a connection with an invalid token without any reply,
	// so a drop before the authentication reply is checked once over REST.
	authed := false
	for {
		f, err := q.next(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if !authed {
				if aerr := s.sessionRejected(ctx); aerr != nil {
					return aerr
				}
			}
			var ne net.Error
			switch {
			case errors.As(err, &ne) && ne.Timeout():
				return errors.New("no ping from the server")
			case websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseNoStatusReceived):
				return errors.New("closed by the server")
			}
			return err
		}
		if f.tick {
			if err := w.retryReactions(ctx); err != nil {
				return err
			}
			armRetry()
			continue
		}
		data := f.data
		w.alive = f.at
		if ev, err := model.WebSocketEventFromJSON(bytes.NewReader(data)); err == nil && ev.IsValid() {
			var err error
			switch ev.EventType() {
			case model.WebsocketEventPosted:
				err = w.posted(ctx, ev)
			case model.WebsocketEventReactionAdded:
				if w.reactions {
					err = w.reactionAdded(ctx, ev)
				}
			}
			if err != nil {
				return &watchOutputError{err: err}
			}
			continue
		}
		var r model.WebSocketResponse
		if json.Unmarshal(data, &r) == nil && r.IsValid() && r.SeqReply == 1 {
			if r.Status != model.StatusOk {
				return errWatchAuth
			}
			authed = true
			if err := w.afterAuth(ctx, prevAlive); err != nil {
				return err
			}
			armRetry()
		}
	}
}

// sessionRejected asks the server whether the session is still valid. Only a
// 401 counts: any other outcome (network error, 5xx, 429) leaves the caller
// to reconnect with a pause.
func (s *Session) sessionRejected(ctx context.Context) error {
	_, resp, err := s.Client.GetMe(ctx, "")
	if err != nil && resp != nil && resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("%w: %w", errWatchAuth, err)
	}
	return nil
}

// queuedFrame is a message read from the WebSocket and when it arrived.
type queuedFrame struct {
	data []byte
	at   time.Time
	// tick marks a wake-up by the timer of a pending catch-up, not a frame.
	tick bool
}

// frameQueue passes frames from the reader goroutine to the main loop without
// ever blocking the reader: a full buffer would stop it from answering pings.
type frameQueue struct {
	mu     sync.Mutex
	frames []queuedFrame
	err    error
	ticked bool
	wake   chan struct{}
}

func newFrameQueue() *frameQueue { return &frameQueue{wake: make(chan struct{}, 1)} }

func (q *frameQueue) signal() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

func (q *frameQueue) push(f queuedFrame) {
	q.mu.Lock()
	q.frames = append(q.frames, f)
	q.mu.Unlock()
	q.signal()
}

// tick queues a wake-up of the main loop behind the frames already waiting.
func (q *frameQueue) tick() {
	q.mu.Lock()
	q.ticked = true
	q.mu.Unlock()
	q.signal()
}

// fail records the end of the connection; frames already queued are still
// delivered first.
func (q *frameQueue) fail(err error) {
	q.mu.Lock()
	q.err = err
	q.mu.Unlock()
	q.signal()
}

// next waits for the oldest frame; when none is queued, for a pending tick,
// then for the error that ended the reader. The tick comes after the queued
// frames, so a catch-up never overtakes events already received.
func (q *frameQueue) next(ctx context.Context) (queuedFrame, error) {
	for {
		q.mu.Lock()
		if len(q.frames) > 0 {
			f := q.frames[0]
			q.frames[0] = queuedFrame{}
			q.frames = q.frames[1:]
			q.mu.Unlock()
			return f, nil
		}
		err := q.err
		ticked := q.ticked
		q.ticked = false
		q.mu.Unlock()
		if ticked {
			return queuedFrame{tick: true}, nil
		}
		if err != nil {
			return queuedFrame{}, err
		}
		select {
		case <-q.wake:
		case <-ctx.Done():
			return queuedFrame{}, ctx.Err()
		}
	}
}

// parseUrgency maps --urgency to the freedesktop urgency. It runs before
// anything connects, so a typo or a missing --notify costs no request.
func parseUrgency(v string, set, notify bool) (urgency, error) {
	if set && !notify {
		return urgencyLow, clierr.New(clierr.CodeUsage, "--urgency needs --notify")
	}
	switch v {
	case "low":
		return urgencyLow, nil
	case "normal":
		return urgencyNormal, nil
	case "critical":
		return urgencyCritical, nil
	}
	return urgencyLow, clierr.New(clierr.CodeUsage, "invalid --urgency %q: use low, normal or critical", v)
}
