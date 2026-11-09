// Package cli implements the mm-cli commands.
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/VitalyOstanin/mm-cli/internal/auth"
	"github.com/VitalyOstanin/mm-cli/internal/client"
	"github.com/VitalyOstanin/mm-cli/internal/clierr"
	"github.com/VitalyOstanin/mm-cli/internal/config"
	"github.com/VitalyOstanin/mm-cli/internal/render"
)

// App holds the process environment of one command run.
type App struct {
	In          io.Reader
	Out, Err    io.Writer
	Store       config.Store
	Env         func(string) string
	Now         func() time.Time
	Location    *time.Location
	OpenBrowser func(string) error
	IsTerminal  func() bool
	// Sleep waits for d or until ctx is done; nil means a real timer.
	Sleep       func(ctx context.Context, d time.Duration) error
	Version     string
	DefaultRate config.RateLimit

	profile string
	team    string
	json    bool
}

// NewApp returns an App bound to the real process.
func NewApp(version string) (*App, error) {
	st, err := config.DefaultStore()
	if err != nil {
		return nil, err
	}
	return &App{
		In: os.Stdin, Out: os.Stdout, Err: os.Stderr,
		Store:       st,
		Env:         os.Getenv,
		Now:         time.Now,
		Location:    time.Local,
		OpenBrowser: auth.OpenBrowser,
		IsTerminal:  func() bool { return term.IsTerminal(int(os.Stdin.Fd())) },
		Version:     version,
		DefaultRate: config.RateLimit{PerSecond: client.DefaultPerSecond, Burst: client.DefaultBurst},
	}, nil
}

// NewRootCommand builds the command tree.
func NewRootCommand(app *App) *cobra.Command {
	root := &cobra.Command{
		Use:           "mm-cli",
		Short:         "Mattermost from the command line",
		Version:       app.Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	// Defined before cobra adds its own --version so that -V works too.
	root.Flags().BoolP("version", "V", false, "print the version")
	root.PersistentFlags().StringVar(&app.profile, "profile", "", "profile name (default: $MM_PROFILE or the default profile)")
	root.PersistentFlags().StringVar(&app.team, "team", "", "team name (default: the profile's team)")
	root.PersistentFlags().BoolVar(&app.json, "json", false, "print JSON")
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return clierr.Usage("%v", err) })
	root.AddCommand(
		newAuthCmd(app), newProfileCmd(app), newChannelCmd(app), newPostCmd(app),
		newFileCmd(app), newPinCmd(app), newBookmarkCmd(app), newSavedCmd(app),
		newSendCmd(app, "send"), newListCmd(app, "messages"), newWatchCmd(app),
	)
	return root
}

// Execute runs the command line and returns the exit code.
func Execute(ctx context.Context, app *App, args []string) int {
	// Text output carries messages, file and channel names from the server;
	// keep their control characters away from the terminal. JSON escapes
	// them itself, so --json output passes through unchanged.
	out, errw := app.Out, app.Err
	textMode := func() bool { return !app.json }
	safeOut, safeErr := render.NewSafeWriter(out, textMode), render.NewSafeWriter(errw, textMode)
	app.Out, app.Err = safeOut, safeErr
	defer func() {
		_ = safeOut.Flush()
		_ = safeErr.Flush()
		app.Out, app.Err = out, errw
	}()
	root := NewRootCommand(app)
	root.SetArgs(args)
	root.SetIn(app.In)
	root.SetOut(app.Out)
	root.SetErr(app.Err)
	err := root.ExecuteContext(ctx)
	_ = safeOut.Flush()
	// A run stopped by SIGINT or SIGTERM exits with 130 or 143, whatever the
	// command returned: watch stops without an error, others see "canceled".
	if cause := context.Cause(ctx); ctx.Err() != nil {
		if code := clierr.ExitCode(cause); code == clierr.CodeInterrupted || code == clierr.CodeTerminated {
			err = cause
		}
	}
	if err == nil {
		return clierr.CodeOK
	}
	if clierr.ExitCode(err) == clierr.CodeError && strings.HasPrefix(err.Error(), "unknown command") {
		err = clierr.Usage("%v", err)
	}
	// Cobra parses no flags when the command is unknown, and stops at the
	// first bad flag; look for --json in the arguments then.
	if !app.json && !root.PersistentFlags().Changed("json") {
		app.json = jsonRequested(args)
	}
	app.PrintError(err)
	return clierr.ExitCode(err)
}

// jsonRequested reports whether the arguments before "--" turn on --json.
func jsonRequested(args []string) bool {
	on := false
	for _, a := range args {
		if a == "--" {
			break
		}
		if a == "--json" {
			on = true
		} else if v, ok := strings.CutPrefix(a, "--json="); ok {
			if b, err := strconv.ParseBool(v); err == nil {
				on = b
			}
		}
	}
	return on
}

// PrintError writes the error to stderr as text or, with --json, as an object.
func (a *App) PrintError(err error) {
	if a.json {
		enc := json.NewEncoder(a.Err)
		enc.SetEscapeHTML(false)
		_ = enc.Encode(map[string]any{
			"error": map[string]any{"code": clierr.ExitCode(err), "message": err.Error()},
		})
		return
	}
	fmt.Fprintln(a.Err, "mm-cli:", err)
}

// sleep waits for d or until ctx is done.
func (a *App) sleep(ctx context.Context, d time.Duration) error {
	if a.Sleep != nil {
		return a.Sleep(ctx, d)
	}
	return client.SleepCtx(ctx, d)
}

// printJSON writes v to stdout.
func (a *App) printJSON(v any) error { return render.JSON(a.Out, v) }

// printDone reports a finished change: r as JSON with --json, otherwise the
// formatted text line.
func (a *App) printDone(r actionResult, format string, args ...any) error {
	if a.json {
		return a.printJSON(r)
	}
	fmt.Fprintf(a.Out, format+"\n", args...)
	return nil
}

const unlimited = int(^uint(0) >> 1)

func argsRange(minN, maxN int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) >= minN && len(args) <= maxN {
			return nil
		}
		want := fmt.Sprintf("%d to %d", minN, maxN)
		switch {
		case minN == maxN:
			want = strconv.Itoa(minN)
		case maxN == unlimited:
			want = fmt.Sprintf("at least %d", minN)
		}
		return clierr.Usage("%s expects %s argument(s), got %d; see --help", cmd.CommandPath(), want, len(args))
	}
}

// messageText returns args[i], reading stdin when it is "-".
func (a *App) messageText(args []string, i int) (string, error) {
	if len(args) <= i {
		return "", nil
	}
	if args[i] != "-" {
		if !utf8.ValidString(args[i]) {
			return "", clierr.Usage("the message is not valid UTF-8; convert it first, e.g. with iconv -t UTF-8")
		}
		return args[i], nil
	}
	const limit = 1 << 20
	data, err := io.ReadAll(io.LimitReader(a.In, limit+1))
	if err != nil {
		return "", err
	}
	if len(data) > limit {
		return "", clierr.Usage("the message on stdin is larger than 1 MiB")
	}
	if !utf8.Valid(data) {
		return "", clierr.Usage("the message on stdin is not valid UTF-8; convert it first, e.g. with iconv -t UTF-8")
	}
	return strings.TrimRight(string(data), "\r\n"), nil
}

// parseSince accepts RFC 3339 or a duration back from now: 90m, 2h, 3d.
func parseSince(s string, now time.Time) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	var d time.Duration
	var err error
	if n, ok := strings.CutSuffix(s, "d"); ok {
		var days int
		days, err = strconv.Atoi(n)
		// Check before multiplying: a large count would overflow
		// time.Duration and wrap around to a small positive value.
		if err == nil && days > int(math.MaxInt64/int64(24*time.Hour)) {
			err = strconv.ErrRange
		}
		d = time.Duration(days) * 24 * time.Hour
	} else {
		d, err = time.ParseDuration(s)
	}
	if err != nil || d <= 0 {
		return time.Time{}, clierr.Usage("invalid --since %q: use RFC 3339 (2026-01-02T15:04:05Z) or a duration such as 90m, 2h, 3d", s)
	}
	return now.Add(-d), nil
}

// actionResult is the --json output of a command that changes something on
// the server but has no changed object to print. Field names are a public
// interface.
type actionResult struct {
	Action    string `json:"action"`
	ID        string `json:"id,omitempty"`
	ChannelID string `json:"channel_id,omitempty"`
	PostID    string `json:"post_id,omitempty"`
	OldFileID string `json:"old_file_id,omitempty"`
	FileID    string `json:"file_id,omitempty"`
	Profile   string `json:"profile,omitempty"`
}
