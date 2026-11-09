package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/spf13/cobra"

	"github.com/VitalyOstanin/mm-cli/internal/clierr"
	"github.com/VitalyOstanin/mm-cli/internal/render"
)

// presetHour is the time of day of the app's presets: Tomorrow at 9:00 and
// Monday at 9:00.
const presetHour = 9

const whenHelp = `<when> is one of:
  tomorrow             tomorrow at 9:00
  monday               the next Monday after today at 9:00 (also: next-monday)
  YYYY-MM-DD HH:MM     that time
  RFC 3339             2026-11-12T14:30:00+03:00
The presets and YYYY-MM-DD HH:MM use the time zone of your Mattermost
profile (Settings > Display > Timezone), as the Mattermost app does, or the
local one when the profile has none. The time must be in the future.`

// directChannelsKey is the key under which the server lists the scheduled
// messages of direct and group channels.
const directChannelsKey = "directChannels"

// errNotAvailable is the message for servers where scheduled messages are
// turned off, unlicensed, or not implemented.
const errNotAvailable = "scheduled messages are not available on this server (turned off, no license, or an older version)"

// whenSyntax rejects a <when> that no form matches, before any request.
func whenSyntax(s string) error {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "tomorrow", "monday", "next-monday":
		return nil
	}
	if _, err := time.Parse(time.RFC3339, s); err == nil {
		return nil
	}
	if _, err := time.Parse("2006-01-02 15:04", s); err == nil {
		return nil
	}
	return clierr.Usage("invalid time %q: use tomorrow, monday, YYYY-MM-DD HH:MM or RFC 3339", s)
}

// needsZone reports whether parsing s needs the time zone of the profile.
func needsZone(s string) bool {
	_, err := time.Parse(time.RFC3339, s)
	return err != nil
}

// parseWhen turns <when> into a time; loc is the zone of the presets and of
// YYYY-MM-DD HH:MM.
func parseWhen(s string, now time.Time, loc *time.Location) (time.Time, error) {
	local := now.In(loc)
	var t time.Time
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "tomorrow":
		t = time.Date(local.Year(), local.Month(), local.Day()+1, presetHour, 0, 0, 0, loc)
	case "monday", "next-monday":
		days := (8 - int(local.Weekday())) % 7
		if days == 0 {
			days = 7
		}
		t = time.Date(local.Year(), local.Month(), local.Day()+days, presetHour, 0, 0, 0, loc)
	default:
		var err error
		if t, err = time.Parse(time.RFC3339, s); err != nil {
			if t, err = time.ParseInLocation("2006-01-02 15:04", s, loc); err != nil {
				return time.Time{}, clierr.Usage("invalid time %q: use tomorrow, monday, YYYY-MM-DD HH:MM or RFC 3339", s)
			}
		}
	}
	if !t.After(now) {
		return time.Time{}, clierr.Usage("the time %s is not in the future", t.In(loc).Format("2006-01-02 15:04 MST"))
	}
	return t, nil
}

// scheduleZone returns the time zone of the user's Mattermost profile and its
// IANA name. Without a usable zone in the profile it falls back to the local
// zone; the name is then empty unless that zone has a real IANA name.
func (s *Session) scheduleZone(ctx context.Context) (*time.Location, string, error) {
	me, resp, err := s.Client.GetMe(ctx, "")
	if err != nil {
		return nil, "", clierr.API(resp, err)
	}
	if name := model.GetPreferredTimezone(me.Timezone); name != "" {
		if loc, err := time.LoadLocation(name); err == nil {
			return loc, name, nil
		}
	}
	loc := s.app.Location
	if name := loc.String(); name != "" && name != "Local" {
		return loc, name, nil
	}
	return loc, "", nil
}

// scheduledErr classifies an error of the scheduled-post API: servers with
// the feature turned off, without a license, or without the routes at all
// become exit code 5.
func scheduledErr(resp *model.Response, err error) error {
	var ae *model.AppError
	if errors.As(err, &ae) {
		switch ae.Id {
		case "api.scheduled_posts.feature_disabled", "api.scheduled_posts.license_error", "api.context.404.app_error":
			return clierr.Wrap(clierr.CodeNotFound, err, errNotAvailable)
		}
	}
	if resp != nil && resp.StatusCode == http.StatusNotImplemented {
		return clierr.Wrap(clierr.CodeNotFound, err, errNotAvailable)
	}
	return clierr.API(resp, err)
}

// scheduleOpts are the scheduling flags of send.
type scheduleOpts struct {
	when   string
	weekly bool
}

// check rejects flag combinations and malformed times before anything is
// sent; set tells whether --schedule was given at all.
func (o scheduleOpts) check(set bool, files int, now time.Time) error {
	if set {
		if err := whenSyntax(o.when); err != nil {
			return err
		}
		// A time with its own zone can be checked against now already.
		if !needsZone(o.when) {
			if _, err := parseWhen(o.when, now, time.UTC); err != nil {
				return err
			}
		}
	}
	if o.weekly && o.when == "" {
		return clierr.Usage("--repeat-weekly needs --schedule")
	}
	if o.weekly && files > 0 {
		return clierr.Usage("a message that repeats weekly cannot have files")
	}
	return nil
}

// resolve returns the send time and, for a weekly message, the IANA zone the
// server repeats it in.
func (o scheduleOpts) resolve(ctx context.Context, s *Session) (time.Time, string, error) {
	loc, zone := s.app.Location, ""
	if needsZone(o.when) || o.weekly {
		var err error
		if loc, zone, err = s.scheduleZone(ctx); err != nil {
			return time.Time{}, "", err
		}
	}
	if o.weekly && zone == "" {
		return time.Time{}, "", clierr.Usage("--repeat-weekly needs a time zone: set one in Mattermost (Settings > Display > Timezone)")
	}
	t, err := parseWhen(o.when, s.app.Now(), loc)
	if err != nil {
		return time.Time{}, "", err
	}
	if !o.weekly {
		zone = ""
	}
	return t, zone, nil
}

// schedule creates a scheduled message and prints it.
func (s *Session) schedule(ctx context.Context, o scheduleOpts, sp *model.ScheduledPost) error {
	at, zone, err := o.resolve(ctx, s)
	if err != nil {
		return err
	}
	sp.ScheduledAt = at.UnixMilli()
	if o.weekly {
		sp.RepeatType = model.ScheduledPostRepeatTypeWeekly
		sp.RepeatTimezone = zone
	}
	created, resp, err := s.Client.CreateScheduledPost(ctx, sp)
	if err != nil {
		return scheduledErr(resp, err)
	}
	if s.app.json {
		out, err := s.renderScheduled(ctx, []*model.ScheduledPost{created})
		if err != nil {
			return err
		}
		return s.app.printJSON(out[0])
	}
	fmt.Fprintf(s.app.Out, "Scheduled: %s for %s\n", created.Id, time.UnixMilli(created.ScheduledAt).In(s.app.Location).Format("2006-01-02 15:04 MST"))
	return nil
}

func newScheduledCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "scheduled",
		Short: "Scheduled messages: list, edit, reschedule, send now, delete",
		Long: `Scheduled messages, as in Drafts > Scheduled of the Mattermost app. Create one
with "mm-cli send <channel> <text> --schedule <when>"; the server sends it at
that time. A scheduled message is identified by its id (26 characters, the
"id" field of mm-cli scheduled list --json).

A message the server could not send stays in the list with the reason in
"error" (for example channel_archived, no_channel_member, thread_deleted);
it cannot be rescheduled, only deleted and created again.

The server must have scheduled messages turned on and a license (Entry,
Professional or Enterprise); otherwise every command exits with code 5.

A scheduled message in --json output is {"id", "channel_id", "channel" (an
address mm-cli accepts: team/name, @user, or an id), "root_id", "message",
"files" (array of {"id", "name", "size", "mime"}), "scheduled_at",
"create_at", "update_at" (RFC 3339, UTC), "repeat" ("" or "weekly"),
"error" ("" or the server's reason)}.`,
		Example: `  mm-cli send town-square "standup in 5 minutes" --schedule "2026-11-12 09:55"
  mm-cli scheduled list
  mm-cli scheduled send <scheduled-id> --yes`,
	}
	cmd.AddCommand(newScheduledListCmd(app), newScheduledEditCmd(app), newScheduledRescheduleCmd(app),
		newScheduledSendCmd(app), newScheduledDeleteCmd(app))
	return cmd
}

// collectScheduled returns the user's scheduled messages in teams and in
// direct and group channels, each once, by send time.
func collectScheduled(ctx context.Context, s *Session, teams []*model.Team) ([]*model.ScheduledPost, error) {
	seen := map[string]bool{}
	var out []*model.ScheduledPost
	add := func(list []*model.ScheduledPost) {
		for _, sp := range list {
			if sp != nil && !seen[sp.Id] {
				seen[sp.Id] = true
				out = append(out, sp)
			}
		}
	}
	for i, t := range teams {
		// Direct and group channels are the same for every team: ask once.
		m, resp, err := s.Client.GetUserScheduledPosts(ctx, t.Id, i == 0)
		if err != nil {
			return nil, scheduledErr(resp, err)
		}
		add(m[t.Id])
		add(m[directChannelsKey])
	}
	slices.SortFunc(out, func(a, b *model.ScheduledPost) int {
		return cmp.Or(cmp.Compare(a.ScheduledAt, b.ScheduledAt), strings.Compare(a.Id, b.Id))
	})
	return out, nil
}

// userTeams returns every team of the user, whatever --team says: a
// scheduled message is found by id in any of them.
func userTeams(ctx context.Context, s *Session) ([]*model.Team, error) {
	teams, resp, err := s.Client.GetTeamsForUser(ctx, s.Profile.UserID, "")
	if err != nil {
		return nil, clierr.API(resp, err)
	}
	return teams, nil
}

// findScheduled returns the user's scheduled message with id; the API has no
// lookup by id, so it lists them.
func findScheduled(ctx context.Context, s *Session, id string) (*model.ScheduledPost, error) {
	teams, err := userTeams(ctx, s)
	if err != nil {
		return nil, err
	}
	list, err := collectScheduled(ctx, s, teams)
	if err != nil {
		return nil, err
	}
	for _, sp := range list {
		if sp.Id == id {
			return sp, nil
		}
	}
	return nil, clierr.New(clierr.CodeNotFound, "no scheduled message %s", id)
}

// renderScheduled turns scheduled messages into output records with channel
// addresses from one channel listing.
func (s *Session) renderScheduled(ctx context.Context, list []*model.ScheduledPost) ([]render.Scheduled, error) {
	out := make([]render.Scheduled, 0, len(list))
	if len(list) == 0 {
		return out, nil
	}
	teams, err := userTeams(ctx, s)
	if err != nil {
		return nil, err
	}
	chans, err := listChannels(ctx, s, teams, true)
	if err != nil {
		return nil, err
	}
	channels := make(map[string]teamChannel, len(chans))
	var ids []string
	for _, tc := range chans {
		channels[tc.ch.Id] = tc
	}
	for _, sp := range list {
		if tc, ok := channels[sp.ChannelId]; ok && tc.ch.Type == model.ChannelTypeDirect {
			ids = append(ids, tc.ch.GetOtherUserIdForDM(s.Profile.UserID))
		}
	}
	var names map[string]string
	if len(ids) > 0 {
		names = s.usernames(ctx, ids)
	}
	for _, sp := range list {
		r := render.ScheduledFrom(sp)
		r.Channel, _, r.Label = threadChannel(channels, sp.ChannelId, len(teams) > 1, names, s.Profile.UserID)
		out = append(out, r)
	}
	return out, nil
}

// printScheduled prints one scheduled message.
func (s *Session) printScheduled(ctx context.Context, sp *model.ScheduledPost, verb string) error {
	if !s.app.json {
		fmt.Fprintf(s.app.Out, "%s: %s for %s\n", verb, sp.Id, time.UnixMilli(sp.ScheduledAt).In(s.app.Location).Format("2006-01-02 15:04 MST"))
		return nil
	}
	out, err := s.renderScheduled(ctx, []*model.ScheduledPost{sp})
	if err != nil {
		return err
	}
	return s.app.printJSON(out[0])
}

func newScheduledListCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Show your scheduled messages, soonest first",
		Long: `Show your scheduled messages, the one to be sent soonest first: send time,
channel, "weekly" for a repeating message, "failed: <reason>" for one the
server could not send, the id, then the text and attachments.

It covers the selected team (--team, or the profile's team) or all your
teams, and always your direct and group channels.

With --json it prints an array of scheduled messages (mm-cli scheduled
--help lists the fields); [] when there are none. Exit code 5 when the server
has no scheduled messages.`,
		Example: `  mm-cli scheduled list
  mm-cli scheduled list --team myteam --json`,
		Args: argsRange(0, 0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := app.openSession()
			if err != nil {
				return err
			}
			defer s.Close()
			ctx := cmd.Context()
			teams, err := listTeams(ctx, s)
			if err != nil {
				return err
			}
			list, err := collectScheduled(ctx, s, teams)
			if err != nil {
				return err
			}
			out, err := s.renderScheduled(ctx, list)
			if err != nil {
				return err
			}
			if app.json {
				return app.printJSON(out)
			}
			return render.ScheduledText(app.Out, out, app.Location)
		},
	}
}

func newScheduledEditCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "edit <scheduled-id> [<text> | -]",
		Short: "Replace the text of a scheduled message",
		Long: `Replace the whole text of a scheduled message; '-' reads it from stdin. The
send time, attachments and repeat stay as they are (the server does not
change attachments of a scheduled message).

With --json it prints the scheduled message (mm-cli scheduled --help). Exit
code 2 for an invalid id or empty text, 5 when there is no such scheduled
message or the server has none.`,
		Example: `  mm-cli scheduled edit <scheduled-id> "new text"
  printf 'line 1\nline 2\n' | mm-cli scheduled edit <scheduled-id> - --json`,
		Args: argsRange(2, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validID(args[0], "scheduled message id"); err != nil {
				return err
			}
			text, err := app.messageText(args, 1)
			if err != nil {
				return err
			}
			if text == "" {
				return clierr.Usage("the new text is empty")
			}
			return updateScheduled(cmd.Context(), app, args[0], "", "Edited", func(sp *model.ScheduledPost) error {
				sp.Message = text
				return nil
			})
		},
	}
}

func newScheduledRescheduleCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "reschedule <scheduled-id> <when>",
		Short: "Change the send time of a scheduled message",
		Long: `Change the send time of a scheduled message. A weekly message keeps
repeating from the new time. A message the server could not send ("failed"
in scheduled list) cannot be rescheduled, as in the Mattermost app: delete
it and schedule it again.

` + whenHelp + `

With --json it prints the scheduled message (mm-cli scheduled --help). Exit
code 2 for an invalid id or time, a time in the past, or a failed message;
5 when there is no such scheduled message or the server has none.`,
		Example: `  mm-cli scheduled reschedule <scheduled-id> tomorrow
  mm-cli scheduled reschedule <scheduled-id> "2026-11-12 14:30" --json`,
		Args: argsRange(2, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validID(args[0], "scheduled message id"); err != nil {
				return err
			}
			if err := whenSyntax(args[1]); err != nil {
				return err
			}
			return updateScheduled(cmd.Context(), app, args[0], args[1], "Rescheduled", func(sp *model.ScheduledPost) error {
				if sp.ErrorCode != "" {
					return clierr.Usage("the server could not send this message (%s); it cannot be rescheduled: delete it and schedule it again", sp.ErrorCode)
				}
				return nil
			})
		},
	}
}

// updateScheduled finds a scheduled message, lets change modify it, saves it
// and prints it. A non-empty when sets the send time after change.
func updateScheduled(ctx context.Context, app *App, id, when, verb string, change func(*model.ScheduledPost) error) error {
	s, err := app.openSession()
	if err != nil {
		return err
	}
	defer s.Close()
	sp, err := findScheduled(ctx, s, id)
	if err != nil {
		return err
	}
	if err := change(sp); err != nil {
		return err
	}
	if when != "" {
		at, _, err := scheduleOpts{when: when}.resolve(ctx, s)
		if err != nil {
			return err
		}
		sp.ScheduledAt = at.UnixMilli()
	}
	updated, resp, err := s.Client.UpdateScheduledPost(ctx, sp)
	if err != nil {
		return scheduledErr(resp, err)
	}
	return s.printScheduled(ctx, updated, verb)
}

// canSendNow reports why a scheduled message cannot be sent now, as the
// Mattermost app decides: weekly messages and failed ones, except for the
// errors that a new attempt may fix.
func canSendNow(sp *model.ScheduledPost) error {
	if sp.RepeatType != "" {
		return clierr.Usage("a message that repeats weekly cannot be sent now, as in the Mattermost app")
	}
	switch sp.ErrorCode {
	case "", model.ScheduledPostErrorUnknownError, model.ScheduledPostErrorUnableToSend:
		return nil
	}
	return clierr.Usage("the server could not send this message (%s); it cannot be sent now: delete it and send the text again", sp.ErrorCode)
}

func newScheduledSendCmd(app *App) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "send <scheduled-id>",
		Short: "Send a scheduled message now",
		Long: `Send a scheduled message now ("Send now" in the Mattermost app): the message
is posted with its text, attachments, thread and priority, then the
scheduled message is deleted. When posting fails, the scheduled message
stays. When posting succeeds but the deletion fails, the message is sent and
the error names the scheduled message still left.

Not possible for a message that repeats weekly, nor for one the server could
not send, unless the reason was unknown or unable_to_send (exit code 2), as
in the app.

Asks for confirmation on a terminal; without a terminal it needs --yes,
otherwise exit code 2. With --json it prints the sent message (mm-cli help
output). Exit code 4 without the right to post in the channel, 5 when there
is no such scheduled message or the server has none.`,
		Example: `  mm-cli scheduled send <scheduled-id> --yes
  mm-cli scheduled send <scheduled-id> --yes --json`,
		Args: argsRange(1, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validID(args[0], "scheduled message id"); err != nil {
				return err
			}
			ctx := cmd.Context()
			if err := app.confirm(ctx, "Send the scheduled message now?", yes); err != nil {
				return err
			}
			s, err := app.openSession()
			if err != nil {
				return err
			}
			defer s.Close()
			sp, err := findScheduled(ctx, s, args[0])
			if err != nil {
				return err
			}
			if err := canSendNow(sp); err != nil {
				return err
			}
			p, err := sp.ToPost()
			if err != nil {
				return clierr.New(clierr.CodeError, "%v", err)
			}
			post, resp, err := s.Client.CreatePost(ctx, p)
			if err != nil {
				return clierr.API(resp, err)
			}
			if app.json {
				if err := app.printJSON(s.ownPost(post)); err != nil {
					return err
				}
			} else {
				fmt.Fprintf(app.Out, "Sent: %s\n", post.Id)
			}
			if _, resp, err := s.Client.DeleteScheduledPost(ctx, sp.Id); err != nil {
				e := scheduledErr(resp, err)
				return clierr.Wrap(clierr.ExitCode(e), e, "sent as %s, but the scheduled message %s was not deleted: %v", post.Id, sp.Id, e)
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask for confirmation")
	return cmd
}

func newScheduledDeleteCmd(app *App) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <scheduled-id>...",
		Short: "Delete scheduled messages, one request at a time",
		Long: `Delete scheduled messages by id, one request each, in the order given; they
are not sent. Deletion cannot be undone.

Asks for confirmation on a terminal; without a terminal it needs --yes,
otherwise exit code 2 and nothing is deleted. All ids are checked before the
first request: one invalid id is exit code 2 and nothing is deleted.

Text output prints "Deleted: <id>" as each one goes. With --json it prints
an array of {"action": "deleted", "id": "<scheduled id>"} at the end; when a
deletion fails, the array of those already deleted is printed first, then the
error names the failed id. Exit code 5 for an unknown id or a server without
scheduled messages.`,
		Example: `  mm-cli scheduled delete <scheduled-id> --yes
  mm-cli scheduled delete <scheduled-id> <scheduled-id> --yes --json`,
		Args: argsRange(1, unlimited),
		RunE: func(cmd *cobra.Command, args []string) error {
			for _, id := range args {
				if err := validID(id, "scheduled message id"); err != nil {
					return err
				}
			}
			ctx := cmd.Context()
			if err := app.confirm(ctx, fmt.Sprintf("Delete %d scheduled message(s)?", len(args)), yes); err != nil {
				return err
			}
			s, err := app.openSession()
			if err != nil {
				return err
			}
			defer s.Close()
			var done doneList
			for _, id := range args {
				if _, resp, err := s.Client.DeleteScheduledPost(ctx, id); err != nil {
					if app.json && len(done) > 0 {
						_ = done.finish(app)
					}
					return done.fail("delete "+id, scheduledErr(resp, err))
				}
				done.add(app, actionResult{Action: "deleted", ID: id}, "Deleted")
			}
			return done.finish(app)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask for confirmation")
	return cmd
}
