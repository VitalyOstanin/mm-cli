package cli

import (
	"fmt"
	"os"
	"slices"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/spf13/cobra"

	"github.com/VitalyOstanin/mm-cli/internal/clierr"
	"github.com/VitalyOstanin/mm-cli/internal/render"
)

const (
	maxFilesPerPost = 10
	maxPerPage      = 200
)

const channelHelp = `The channel is @me (your direct channel with yourself), @user (a direct
channel, created on first use), a channel name in the default team,
team:channel, or a 26-character channel id.`

func newPostCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "post",
		Short: "Create, read, edit and delete messages",
		Long: `Messages ("posts" in the Mattermost API). Reading commands (get, list, thread,
search, history, saved) never mark anything read; create, edit, restore and
delete change messages on the server. pin and unpin pin a message to its
channel for every member; save and unsave keep it in your saved messages,
visible only to you. Message ids are 26 characters, from the "id"
field of --json output. post create is the same as send, post list the same
as messages.`,
	}
	cmd.AddCommand(newSendCmd(app, "create"), newGetCmd(app), newListCmd(app, "list"), newEditCmd(app), newDeleteCmd(app),
		newThreadCmd(app), newSearchCmd(app), newHistoryCmd(app), newRestoreCmd(app),
		newPinToggleCmd(app, "pin"), newPinToggleCmd(app, "unpin"),
		newSavedToggleCmd(app, "save"), newSavedToggleCmd(app, "unsave"), newSavedListCmd(app))
	return cmd
}

func newSendCmd(app *App, use string) *cobra.Command {
	var files []string
	var reply string
	var sched scheduleOpts
	cmd := &cobra.Command{
		Use:         use + " <channel> [<text> | -] [-f <file>]...",
		Annotations: effect(effectServer),
		Short:       "Send a message with optional attachments",
		Long: `Send a message. The text is an argument or '-' for stdin (the whole input,
newlines kept). -f attaches a local file, up to 10 per message; files are
uploaded first, then the message is created. --reply-to <post-id> posts into the
thread of that message. Sending to @user creates the direct channel when it
does not exist yet.

--schedule <when> schedules the message instead of sending it, as "Schedule
message" in the Mattermost app: the files are uploaded now and the server
sends the message at that time (mm-cli scheduled lists and changes it).
--repeat-weekly sends it again every week at the same time; such a message
cannot have files and needs a time zone in your Mattermost profile.

` + whenHelp + `

A failed send is never repeated automatically, so a message is not sent
twice; after a network error check with mm-cli messages before sending again.

With --json it prints the sent message (mm-cli help output), or with
--schedule the scheduled message (mm-cli scheduled --help) and as text
"Scheduled: <id> for <time>". Exit code 2 for nothing to send, more than 10
files, an unreadable file, an invalid --reply-to id, an invalid or past
--schedule time, or --repeat-weekly with files or without --schedule; 4
without the right to post in the channel; 5 for an unknown channel or user,
or with --schedule on a server without scheduled messages.

` + channelHelp,
		Example: `  mm-cli send @me "note to self"
  mm-cli send @me -f report.pdf -f photo.jpg
  echo "multi-line text" | mm-cli send myteam:town-square -
  mm-cli send town-square "reply" --reply-to <post-id>
  mm-cli send town-square "weekly report is due" --schedule monday --repeat-weekly`,
		Args: argsChannel(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			text, err := app.messageText(args, 1)
			if err != nil {
				return err
			}
			if text == "" && len(files) == 0 {
				return clierr.Usage("nothing to send: pass a message, '-' for stdin, or -f <file>")
			}
			if len(files) > maxFilesPerPost {
				return clierr.Usage("at most %d files per message", maxFilesPerPost)
			}
			if reply != "" {
				if err := validID(reply, "post id"); err != nil {
					return err
				}
			}
			if err := sched.check(cmd.Flags().Changed("schedule"), len(files), app.Now()); err != nil {
				return err
			}
			data := make([][]byte, len(files))
			for i, f := range files {
				if data[i], err = os.ReadFile(f); err != nil {
					return clierr.Usage("%v", err)
				}
			}
			s, err := app.openSession()
			if err != nil {
				return err
			}
			defer s.Close()
			// The send time is resolved before any file is uploaded, so that
			// a past time leaves no orphan files on the server.
			var at time.Time
			var zone string
			if sched.when != "" {
				if at, zone, err = sched.resolve(ctx, s); err != nil {
					return err
				}
			}
			return s.withChannel(ctx, args[0], func(chID string) error {
				var ids []string
				for i, f := range files {
					id, err := s.uploadOne(ctx, data[i], chID, f, f)
					if err != nil {
						return err
					}
					ids = append(ids, id)
				}
				if sched.when != "" {
					return s.schedule(ctx, at, zone, &model.ScheduledPost{Draft: model.Draft{ChannelId: chID, Message: text, RootId: reply, FileIds: ids}})
				}
				post, resp, err := s.Client.CreatePost(ctx, &model.Post{ChannelId: chID, Message: text, RootId: reply, FileIds: ids})
				if err != nil {
					return clierr.API(resp, err)
				}
				if app.json {
					return app.printJSON(s.ownPost(post))
				}
				fmt.Fprintf(app.Out, "Sent: %s\n", post.Id)
				return nil
			})
		},
	}
	cmd.Flags().StringArrayVarP(&files, "file", "f", nil, fmt.Sprintf("attach a file (repeatable, up to %d)", maxFilesPerPost))
	cmd.Flags().StringVarP(&reply, "reply-to", "r", "", "reply in the thread of this post id")
	cmd.Flags().StringVar(&sched.when, "schedule", "", "schedule the message for <when> instead of sending it")
	cmd.Flags().BoolVar(&sched.weekly, "repeat-weekly", false, "with --schedule: send it again every week")
	return cmd
}

func newGetCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:         "get <post-id>",
		Annotations: effect(effectRead),
		Short:       "Show one message",
		Long: `Show one message by id, root or reply, without marking anything read.

With --json it prints one message object (mm-cli help output). Exit code 2 for
an invalid id, 4 without access to its channel, 5 when it does not exist or
was deleted.`,
		Example: `  mm-cli post get <post-id>
  mm-cli post get <post-id> --json`,
		Args: argsRange(1, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validID(args[0], "post id"); err != nil {
				return err
			}
			s, err := app.openSession()
			if err != nil {
				return err
			}
			defer s.Close()
			p, resp, err := s.Client.GetPost(cmd.Context(), args[0], "")
			if err != nil {
				return clierr.API(resp, err)
			}
			out := render.FromModel(p, s.usernames(cmd.Context(), []string{p.UserId}))
			if app.json {
				return app.printJSON(out)
			}
			return render.PostsText(app.Out, []render.Post{out}, app.Location, 0)
		},
	}
}

func newListCmd(app *App, use string) *cobra.Command {
	var n, truncate int
	var since, before string
	cmd := &cobra.Command{
		Use:         use + " <channel>",
		Annotations: effect(effectRead),
		Short:       "Show recent messages, oldest first",
		Long: `Show the most recent messages of a channel, oldest first. Reading does not
mark the channel as read.

--since keeps only messages after a time (RFC 3339, or 90m, 2h, 3d back from
now); --before shows messages older than a post. Both still show at most -n.
With --json it prints an array of messages (mm-cli help output), oldest first;
[] when there are none. Exit code 2 for an invalid --since or --before, or
both together; 5 for an unknown channel.

` + channelHelp,
		Example: `  mm-cli messages @me
  mm-cli post list town-square -n 50 --since 3d
  mm-cli post list myteam:town-square --json`,
		Args: argsChannel(1, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if err := checkNumber(n); err != nil {
				return err
			}
			if since != "" && before != "" {
				return clierr.Usage("--since and --before cannot be used together")
			}
			if before != "" {
				if err := validID(before, "post id"); err != nil {
					return err
				}
			}
			var sinceMs int64
			if since != "" {
				t, err := parseSince(since, app.Now())
				if err != nil {
					return err
				}
				sinceMs = t.UnixMilli()
			}
			s, err := app.openSession()
			if err != nil {
				return err
			}
			defer s.Close()
			var posts []*model.Post
			err = s.withChannel(ctx, args[0], func(chID string) error {
				var pl *model.PostList
				var resp *model.Response
				var err error
				// --since reads the same newest page as the default case and
				// filters it by creation time: the API's since parameter selects
				// by update time (edited, reacted-to and deleted posts) and
				// takes no page size. The page is ordered by creation time, so
				// any newer match would already be in it.
				if before != "" {
					pl, resp, err = s.Client.GetPostsBefore(ctx, chID, before, 0, n, "", false, false)
				} else {
					pl, resp, err = s.Client.GetPostsForChannel(ctx, chID, 0, n, "", false, false)
				}
				if err != nil {
					return clierr.API(resp, err)
				}
				posts = render.Sorted(pl)
				if since != "" {
					posts = slices.DeleteFunc(posts, func(p *model.Post) bool { return p.CreateAt <= sinceMs })
				}
				return nil
			})
			if err != nil {
				return err
			}
			return s.printPosts(ctx, lastN(posts, n), truncate)
		},
	}
	addNumberFlag(cmd, &n, "messages")
	cmd.Flags().StringVar(&since, "since", "", "only messages after this time")
	cmd.Flags().StringVar(&before, "before", "", "only messages older than this post id")
	addTruncateFlag(cmd, &truncate)
	return cmd
}

func newEditCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:         "edit <post-id> [<text> | -]",
		Annotations: effect(effectServer),
		Short:       "Replace the text of your message",
		Long: `Replace the whole text of your message; '-' reads the new text from stdin.
Attachments stay. The previous text is kept in the edit history
(mm-cli post history) and can be brought back with mm-cli post restore.

With --json it prints the edited message (mm-cli help output). Exit code 2 for
an invalid id or empty text, 4 for a message of someone else, 5 when it does
not exist.`,
		Example: `  mm-cli post edit <post-id> "corrected text"
  printf 'line 1\nline 2\n' | mm-cli post edit <post-id> - --json`,
		Args: argsRange(2, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validID(args[0], "post id"); err != nil {
				return err
			}
			text, err := app.messageText(args, 1)
			if err != nil {
				return err
			}
			if text == "" {
				return clierr.Usage("the new text is empty")
			}
			s, err := app.openSession()
			if err != nil {
				return err
			}
			defer s.Close()
			p, resp, err := s.Client.PatchPost(cmd.Context(), args[0], &model.PostPatch{Message: &text})
			if err != nil {
				return clierr.API(resp, err)
			}
			if app.json {
				return app.printJSON(s.ownPost(p))
			}
			fmt.Fprintf(app.Out, "Edited: %s\n", p.Id)
			return nil
		},
	}
}

func newDeleteCmd(app *App) *cobra.Command {
	var confirmed bool
	cmd := &cobra.Command{
		Use:         "delete <post-id>...",
		Annotations: effect(effectServer),
		Short:       "Delete messages, one request at a time",
		Long: `Delete messages by id, one request each, in the order given. Deletion cannot
be undone from mm-cli. Deleting someone else's message needs the permission
for it (exit code 4 otherwise).

Asks for confirmation on a terminal; without a terminal it needs --confirm,
otherwise exit code 2 and nothing is deleted. All ids are checked before the
first request: one invalid id is exit code 2 and nothing is deleted.

Text output prints "Deleted: <id>" as each message goes. With --json it prints
an array of {"action": "deleted", "id": "<post id>"} at the end; when a
deletion fails, the array of those already deleted is printed first, then the
error names the failed id.`,
		Example: `  mm-cli post delete <post-id> --confirm
  mm-cli post delete <post-id> <post-id> --confirm --json`,
		Args: argsRange(1, unlimited),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validIDs(args, "post id"); err != nil {
				return err
			}
			if err := app.confirm(cmd.Context(), fmt.Sprintf("Delete %d message(s)?", len(args)), confirmed); err != nil {
				return err
			}
			s, err := app.openSession()
			if err != nil {
				return err
			}
			defer s.Close()
			var done doneList
			for _, id := range args {
				if resp, err := s.Client.DeletePost(cmd.Context(), id); err != nil {
					return done.fail(app, "delete "+id, clierr.API(resp, err))
				}
				done.add(app, actionResult{Action: "deleted", ID: id}, "Deleted")
			}
			return done.finish(app)
		},
	}
	addConfirmFlag(cmd, &confirmed)
	return cmd
}
