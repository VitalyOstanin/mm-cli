package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/spf13/cobra"

	"github.com/VitalyOstanin/mm-cli/internal/clierr"
	"github.com/VitalyOstanin/mm-cli/internal/render"
)

// customStatusOut is a custom status in --json output.
type customStatusOut struct {
	Emoji string `json:"emoji"`
	Text  string `json:"text"`
	// Duration is the server's "clear after" choice: "" (don't clear),
	// thirty_minutes, one_hour, four_hours, today, this_week, date_and_time.
	Duration string `json:"duration"`
	// ExpiresAt is RFC 3339 in UTC, "" when it does not expire.
	ExpiresAt string `json:"expires_at"`
}

// userStatus is a user's status in --json output. Field names are a public
// interface.
type userStatus struct {
	User   render.User `json:"user"`
	Status string      `json:"status"`
	Manual bool        `json:"manual"`
	// DNDEndAt is RFC 3339 in UTC when Do not disturb ends by itself, "".
	DNDEndAt       string           `json:"dnd_end_at"`
	LastActivityAt string           `json:"last_activity_at"`
	CustomStatus   *customStatusOut `json:"custom_status,omitempty"`
}

func toCustomStatus(cs *model.CustomStatus, now time.Time) *customStatusOut {
	if cs == nil || (cs.Emoji == "" && cs.Text == "") {
		return nil
	}
	out := &customStatusOut{Emoji: cs.Emoji, Text: cs.Text, Duration: cs.Duration}
	if !cs.ExpiresAt.IsZero() && cs.Duration != "" {
		if cs.ExpiresAt.Before(now) {
			return nil
		}
		out.ExpiresAt = cs.ExpiresAt.UTC().Format(time.RFC3339)
	}
	return out
}

func toUserStatus(st *model.Status, u *model.User, now time.Time) userStatus {
	out := userStatus{User: render.User{ID: st.UserId}, Status: st.Status, Manual: st.Manual, LastActivityAt: rfc3339ms(st.LastActivityAt)}
	if st.Status == model.StatusDnd && st.DNDEndTime > 0 {
		out.DNDEndAt = time.Unix(st.DNDEndTime, 0).UTC().Format(time.RFC3339)
	}
	if u != nil {
		out.User.Username = u.Username
		out.CustomStatus = toCustomStatus(u.GetCustomStatus(), now)
	}
	return out
}

func rfc3339ms(ms int64) string {
	if ms <= 0 {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format(time.RFC3339)
}

func (a *App) printStatuses(list []userStatus) error {
	if a.json {
		return a.printJSON(list)
	}
	for _, s := range list {
		line := fmt.Sprintf("@%s  %s", s.User.Username, s.Status)
		if s.DNDEndAt != "" {
			if t, err := time.Parse(time.RFC3339, s.DNDEndAt); err == nil {
				line += " until " + t.In(a.Location).Format("2006-01-02 15:04")
			}
		}
		fmt.Fprintln(a.Out, line)
		if cs := s.CustomStatus; cs != nil {
			line = "  custom: "
			if cs.Emoji != "" {
				line += ":" + cs.Emoji + ": "
			}
			line += cs.Text
			if t, err := time.Parse(time.RFC3339, cs.ExpiresAt); err == nil {
				line += " (until " + t.In(a.Location).Format("2006-01-02 15:04") + ")"
			}
			fmt.Fprintln(a.Out, strings.TrimRight(line, " "))
		}
	}
	return nil
}

func newStatusCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Your status (online, away, do not disturb, offline) and custom status",
		Long: `Your status and custom status, as in the status menu of the Mattermost app:
Online, Away, Do not disturb (optionally until a time) and Offline, plus a
custom status with an emoji, a text and "clear after". Other users'
statuses can be read.

A status in --json output is {"user": {"id", "username"}, "status" (online,
away, dnd, offline, or ooo for out of office), "manual" (true when set by
hand rather than by activity), "dnd_end_at" (RFC 3339, or "" when Do not
disturb does not end by itself), "last_activity_at", "custom_status"
(present only when one is set: {"emoji", "text", "duration", "expires_at"})}.`,
		Example: `  mm-cli status get
  mm-cli status set dnd --until 1h
  mm-cli status custom set "In a meeting" --emoji calendar --clear-after 1h`,
	}
	cmd.AddCommand(newStatusGetCmd(app), newStatusSetCmd(app), newCustomStatusCmd(app))
	return cmd
}

func newStatusGetCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "get [@user]...",
		Short: "Show your status or other users'",
		Long: `Show statuses: yours without arguments (or with @me), other users' with
@username. A custom status that has expired is not shown.

With --json it prints an array of statuses (mm-cli status --help). Exit code
5 for an unknown user.`,
		Example: `  mm-cli status get
  mm-cli status get @bob @carol --json`,
		Args: argsRange(0, unlimited),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				args = []string{"@me"}
			}
			s, err := app.openSession()
			if err != nil {
				return err
			}
			defer s.Close()
			ctx := cmd.Context()
			users := make([]*model.User, len(args))
			ids := make([]string, len(args))
			for i, a := range args {
				if users[i], err = userArg(ctx, s, a); err != nil {
					return err
				}
				ids[i] = users[i].Id
			}
			sts, resp, err := s.Client.GetUsersStatusesByIds(ctx, ids)
			if err != nil {
				return clierr.API(resp, err)
			}
			byID := make(map[string]*model.Status, len(sts))
			for _, st := range sts {
				byID[st.UserId] = st
			}
			out := make([]userStatus, 0, len(users))
			for _, u := range users {
				st := byID[u.Id]
				if st == nil {
					st = &model.Status{UserId: u.Id, Status: model.StatusOffline}
				}
				out = append(out, toUserStatus(st, u, app.Now()))
			}
			return app.printStatuses(out)
		},
	}
}

// parseUntil turns --until or --clear-after into a time: a duration from now
// (30m, 1h, 2h) or a <when>.
func parseUntil(s string, now time.Time, loc *time.Location) (time.Time, error) {
	if d, err := time.ParseDuration(s); err == nil {
		if d <= 0 {
			return time.Time{}, clierr.Usage("the duration %q must be positive", s)
		}
		return now.Add(d), nil
	}
	return parseWhen(s, now, loc)
}

// untilNeedsZone reports whether s needs the profile's time zone.
func untilNeedsZone(s string) bool {
	if _, err := time.ParseDuration(s); err == nil {
		return false
	}
	return needsZone(s)
}

// untilSyntax rejects a value of --until that no form matches.
func untilSyntax(s string) error {
	if _, err := time.ParseDuration(s); err == nil {
		return nil
	}
	if err := whenSyntax(s); err != nil {
		return clierr.Usage("invalid time %q: use a duration (30m, 1h, 2h), tomorrow, YYYY-MM-DD HH:MM or RFC 3339", s)
	}
	return nil
}

// zoneFor returns the profile's time zone when s needs it, else the App's.
func (s *Session) zoneFor(ctx context.Context, need bool) (*time.Location, error) {
	if !need {
		return s.app.Location, nil
	}
	loc, _, err := s.scheduleZone(ctx)
	return loc, err
}

var statusValues = []string{model.StatusOnline, model.StatusAway, model.StatusDnd, model.StatusOffline}

func newStatusSetCmd(app *App) *cobra.Command {
	var until string
	cmd := &cobra.Command{
		Use:   "set (online | away | dnd | offline)",
		Short: "Set your status",
		Long: `Set your status by hand: online, away, dnd (Do not disturb) or offline. A
status set by hand stays until changed; --until makes Do not disturb end by
itself, as the app's "Don't clear / 30 mins / 1 hour / 2 hours / Tomorrow /
Custom" choice: a duration (30m, 1h, 2h), tomorrow (9:00), YYYY-MM-DD HH:MM
or RFC 3339. Times use the time zone of your Mattermost profile.

With --json it prints your status (mm-cli status --help, without
custom_status). Exit code 2 for another value, --until without dnd, or an
invalid or past time.`,
		Example: `  mm-cli status set away
  mm-cli status set dnd --until 2h
  mm-cli status set dnd --until "2026-11-12 18:00" --json`,
		Args: argsRange(1, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st := strings.ToLower(args[0])
			valid := false
			for _, v := range statusValues {
				valid = valid || st == v
			}
			if !valid {
				return clierr.Usage("unknown status %q: use online, away, dnd or offline", args[0])
			}
			if until != "" && st != model.StatusDnd {
				return clierr.Usage("--until works only with dnd")
			}
			if until != "" {
				if err := untilSyntax(until); err != nil {
					return err
				}
			}
			s, err := app.openSession()
			if err != nil {
				return err
			}
			defer s.Close()
			ctx := cmd.Context()
			req := &model.Status{UserId: s.Profile.UserID, Status: st}
			if until != "" {
				loc, err := s.zoneFor(ctx, untilNeedsZone(until))
				if err != nil {
					return err
				}
				t, err := parseUntil(until, app.Now(), loc)
				if err != nil {
					return err
				}
				req.DNDEndTime = t.Unix()
			}
			got, resp, err := s.Client.UpdateUserStatus(ctx, s.Profile.UserID, req)
			if err != nil {
				return clierr.API(resp, err)
			}
			out := toUserStatus(got, nil, app.Now())
			out.User.Username = s.Profile.Username
			return app.printStatuses([]userStatus{out})
		},
	}
	cmd.Flags().StringVar(&until, "until", "", "with dnd: when Do not disturb ends")
	return cmd
}

// clearAfterDurations maps --clear-after values to the server's durations.
var clearAfterDurations = map[string]string{
	"30m": "thirty_minutes", "1h": "one_hour", "4h": "four_hours", "today": "today", "this-week": "this_week",
}

// customExpiry returns the server's duration and the expiry time of
// --clear-after in loc: end of today, end of this week (Sunday), now plus a
// fixed duration, or a <when>.
func customExpiry(v string, now time.Time, loc *time.Location) (string, time.Time, error) {
	local := now.In(loc)
	endOfDay := func(days int) time.Time {
		return time.Date(local.Year(), local.Month(), local.Day()+days, 23, 59, 59, 0, loc)
	}
	switch v {
	case "today":
		return "today", endOfDay(0), nil
	case "this-week":
		return "this_week", endOfDay((7 - int(local.Weekday())) % 7), nil
	case "30m", "1h", "4h":
		d, _ := time.ParseDuration(v)
		return clearAfterDurations[v], now.Add(d), nil
	}
	t, err := parseWhen(v, now, loc)
	if err != nil {
		return "", time.Time{}, clierr.Usage("invalid --clear-after %q: use 30m, 1h, 4h, today, this-week, YYYY-MM-DD HH:MM or RFC 3339", v)
	}
	return "date_and_time", t, nil
}

func newCustomStatusCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "custom",
		Short: "Set or clear your custom status",
		Long:  `Your custom status: an emoji and a text shown next to your name, as "Set a custom status" in the app.`,
	}
	var emoji, clearAfter string
	set := &cobra.Command{
		Use:   "set [<text>] [--emoji <name>] [--clear-after <when>]",
		Short: "Set your custom status",
		Long: `Set your custom status: a text (up to 100 characters; longer is cut by the
server) and an emoji name without colons (default speech_balloon when only a
text is given). --clear-after is the app's "Clear after": 30m, 1h, 4h,
today (end of the day), this-week (end of Sunday), or a time (YYYY-MM-DD
HH:MM, RFC 3339); without it the status is not cleared. Days and times use
the time zone of your Mattermost profile.

With --json it prints the custom status {"emoji", "text", "duration",
"expires_at"}. Exit code 2 without text and emoji, or for an invalid or past
--clear-after.`,
		Example: `  mm-cli status custom set "In a meeting" --emoji calendar --clear-after 1h
  mm-cli status custom set "On vacation" --emoji palm_tree --clear-after "2026-11-20 09:00"
  mm-cli status custom set --emoji house --json`,
		Args: argsRange(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			text := ""
			if len(args) == 1 {
				text = args[0]
			}
			emoji = strings.Trim(emoji, ":")
			if text == "" && emoji == "" {
				return clierr.Usage("give a text, --emoji, or both")
			}
			if emoji == "" {
				emoji = model.DefaultCustomStatusEmoji
			}
			if clearAfter != "" {
				if _, ok := clearAfterDurations[clearAfter]; !ok {
					if err := whenSyntax(clearAfter); err != nil {
						return clierr.Usage("invalid --clear-after %q: use 30m, 1h, 4h, today, this-week, YYYY-MM-DD HH:MM or RFC 3339", clearAfter)
					}
				}
			}
			s, err := app.openSession()
			if err != nil {
				return err
			}
			defer s.Close()
			ctx := cmd.Context()
			cs := &model.CustomStatus{Emoji: emoji, Text: text}
			if clearAfter != "" {
				needZone := clearAfter == "today" || clearAfter == "this-week" ||
					(clearAfterDurations[clearAfter] == "" && needsZone(clearAfter))
				loc, err := s.zoneFor(ctx, needZone)
				if err != nil {
					return err
				}
				if cs.Duration, cs.ExpiresAt, err = customExpiry(clearAfter, app.Now(), loc); err != nil {
					return err
				}
			}
			got, resp, err := s.Client.UpdateUserCustomStatus(ctx, s.Profile.UserID, cs)
			if err != nil {
				return clierr.API(resp, err)
			}
			out := toCustomStatus(got, app.Now())
			if out == nil {
				out = &customStatusOut{Emoji: got.Emoji, Text: got.Text, Duration: got.Duration}
			}
			if app.json {
				return app.printJSON(out)
			}
			fmt.Fprintf(app.Out, "Custom status: :%s: %s\n", out.Emoji, out.Text)
			return nil
		},
	}
	set.Flags().StringVar(&emoji, "emoji", "", "emoji name, such as calendar")
	set.Flags().StringVar(&clearAfter, "clear-after", "", "30m, 1h, 4h, today, this-week or a time")
	clear := &cobra.Command{
		Use:   "clear",
		Short: "Clear your custom status",
		Long: `Clear your custom status.

With --json it prints {"action": "custom_status_cleared"}.`,
		Example: `  mm-cli status custom clear
  mm-cli status custom clear --json`,
		Args: argsRange(0, 0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := app.openSession()
			if err != nil {
				return err
			}
			defer s.Close()
			if resp, err := s.Client.RemoveUserCustomStatus(cmd.Context(), s.Profile.UserID); err != nil {
				return clierr.API(resp, err)
			}
			return app.printDone(actionResult{Action: "custom_status_cleared"}, "Custom status cleared.")
		},
	}
	cmd.AddCommand(set, clear)
	return cmd
}
