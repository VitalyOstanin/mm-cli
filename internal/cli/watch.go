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
	render.Post
	Channel string `json:"channel"`
}

func newWatchCmd(app *App) *cobra.Command {
	var truncate int
	cmd := &cobra.Command{
		Use:         "watch [<channel>...]",
		Annotations: effect(effectRead),
		Short:       "Print new messages as they arrive",
		Args:        argsChannels(0, unlimited),
		Long: fmt.Sprintf(`Print new messages as they arrive, until interrupted. Without arguments, all
channels you are a member of are watched.

Watching keeps one WebSocket connection open; there is no polling. After the
connection drops it is opened again, waiting 1, 2, 4 ... up to %d seconds, or
as long as the server asks when it answers 429 Too Many Requests.
Watching does not mark channels as read. With --json, each message is one
JSON object per line (mm-cli help output) with an extra "channel" field;
reconnect notices go to stderr. Stopped by SIGINT or SIGTERM, it exits with
code 130 or 143. Exit code 5 for an unknown channel in the arguments.

`, int(watchMaxBackoff/time.Second)) + channelHelp,
		Example: `  mm-cli watch
  mm-cli watch town-square @bob
  mm-cli watch --json @me`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			s, err := app.openSession()
			if err != nil {
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
			return s.watch(ctx, filter, truncate)
		},
	}
	addTruncateFlag(cmd, &truncate)
	return cmd
}

// watch reads `posted` events until ctx is cancelled, reconnecting with a
// growing pause. Every connection goes through the rate limiter of the
// server, and a ban announced by the server (a 429, here or in another run)
// is waited out before connecting again.
func (s *Session) watch(ctx context.Context, filter map[string]bool, truncate int) error {
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
		err := s.watchOnce(ctx, tr, filter, truncate)
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
func (s *Session) watchOnce(ctx context.Context, tr *client.Transport, filter map[string]bool, truncate int) error {
	conn, err := s.dialWebSocket(ctx, tr)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	// The server pings regularly; a connection without pings is dead.
	_ = conn.SetReadDeadline(time.Now().Add(watchPingTimeout))
	conn.SetPingHandler(func(data string) error {
		_ = conn.SetReadDeadline(time.Now().Add(watchPingTimeout))
		err := conn.WriteControl(websocket.PongMessage, []byte(data), time.Now().Add(time.Second))
		var ne net.Error
		if errors.Is(err, websocket.ErrCloseSent) || errors.As(err, &ne) && ne.Timeout() {
			return nil
		}
		return err
	})
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return nil
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
		if ev, err := model.WebSocketEventFromJSON(bytes.NewReader(data)); err == nil && ev.IsValid() {
			if ev.EventType() != model.WebsocketEventPosted {
				continue
			}
			if err := s.printEvent(ev, filter, truncate); err != nil {
				return &watchOutputError{err: err}
			}
			continue
		}
		var r model.WebSocketResponse
		if json.Unmarshal(data, &r) == nil && r.IsValid() && r.SeqReply == 1 && r.Status != model.StatusOk {
			return errWatchAuth
		}
	}
}

// printEvent prints a `posted` event. The event carries the sender's name, so
// no request is made per message.
func (s *Session) printEvent(ev *model.WebSocketEvent, filter map[string]bool, truncate int) error {
	data := ev.GetData()
	raw, _ := data["post"].(string)
	var p model.Post
	if err := json.Unmarshal([]byte(raw), &p); err != nil || p.Id == "" {
		return nil
	}
	if len(filter) > 0 && !filter[p.ChannelId] {
		return nil
	}
	names := map[string]string{}
	if sender, _ := data["sender_name"].(string); sender != "" {
		names[p.UserId] = strings.TrimPrefix(sender, "@")
	}
	channel, _ := data["channel_display_name"].(string)
	if channel == "" {
		channel, _ = data["channel_name"].(string)
	}
	rp := render.FromModel(&p, names)
	if s.app.json {
		b, err := json.Marshal(watchedPost{Post: rp, Channel: channel})
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(s.app.Out, "%s\n", b)
		return err
	}
	if _, err := fmt.Fprintf(s.app.Out, "[%s] ", channel); err != nil {
		return err
	}
	if err := render.PostsText(s.app.Out, []render.Post{rp}, s.app.Location, truncate); err != nil {
		return err
	}
	_, err := fmt.Fprintln(s.app.Out)
	return err
}
