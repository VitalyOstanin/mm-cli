package cli

import (
	"fmt"
	"os"
	"slices"

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
team/channel, or a 26-character channel id.`

func newPostCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{Use: "post", Short: "Create, read, edit and delete messages"}
	cmd.AddCommand(newSendCmd(app, "create"), newGetCmd(app), newListCmd(app, "list"), newEditCmd(app), newDeleteCmd(app),
		newThreadCmd(app), newSearchCmd(app), newHistoryCmd(app), newRestoreCmd(app))
	return cmd
}

func newSendCmd(app *App, use string) *cobra.Command {
	var files []string
	var reply string
	cmd := &cobra.Command{
		Use:   use + " <channel> [<text> | -] [-f <file>]...",
		Short: "Send a message with optional attachments",
		Long:  "Send a message. The text is an argument or '-' for stdin.\n\n" + channelHelp,
		Example: `  mm-cli send @me "note to self"
  mm-cli send @me -f report.pdf -f photo.jpg
  echo "multi-line text" | mm-cli send myteam/town-square -
  mm-cli send town-square "reply" --reply <post-id>`,
		Args: argsRange(1, 2),
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
			return s.withChannel(ctx, args[0], func(chID string) error {
				var ids []string
				for i, f := range files {
					id, err := s.uploadOne(ctx, data[i], chID, f, f)
					if err != nil {
						return err
					}
					ids = append(ids, id)
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
	cmd.Flags().StringVar(&reply, "reply", "", "reply in the thread of this post id")
	return cmd
}

func newGetCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "get <post-id>",
		Short: "Show one message",
		Args:  argsRange(1, 1),
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
		Use:   use + " <channel>",
		Short: "Show recent messages, oldest first",
		Long: `Show the most recent messages of a channel, oldest first. Reading does not
mark the channel as read.

--since keeps only messages after a time (RFC 3339, or 90m, 2h, 3d back from
now); --before shows messages older than a post. Both still show at most -n.

` + channelHelp,
		Example: `  mm-cli messages @me
  mm-cli post list town-square -n 50 --since 3d
  mm-cli post list myteam/town-square --json`,
		Args: argsRange(1, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if err := checkLimit(n); err != nil {
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
	addLimitFlag(cmd, &n, "messages")
	cmd.Flags().StringVar(&since, "since", "", "only messages after this time")
	cmd.Flags().StringVar(&before, "before", "", "only messages older than this post id")
	addTruncateFlag(cmd, &truncate)
	return cmd
}

func newEditCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "edit <post-id> [<text> | -]",
		Short: "Replace the text of your message",
		Args:  argsRange(2, 2),
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
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <post-id>...",
		Short: "Delete messages, one request at a time",
		Args:  argsRange(1, unlimited),
		RunE: func(cmd *cobra.Command, args []string) error {
			for _, id := range args {
				if err := validID(id, "post id"); err != nil {
					return err
				}
			}
			if err := app.confirm(cmd.Context(), fmt.Sprintf("Delete %d message(s)?", len(args)), yes); err != nil {
				return err
			}
			s, err := app.openSession()
			if err != nil {
				return err
			}
			defer s.Close()
			// With --json the deleted ids are printed together at the end, also
			// when a later deletion fails.
			deleted := []actionResult{}
			for _, id := range args {
				if resp, err := s.Client.DeletePost(cmd.Context(), id); err != nil {
					e := clierr.API(resp, err)
					if app.json && len(deleted) > 0 {
						_ = app.printJSON(deleted)
					}
					return clierr.Wrap(clierr.ExitCode(e), e, "delete %s: %v", id, e)
				}
				deleted = append(deleted, actionResult{Action: "deleted", ID: id})
				if !app.json {
					fmt.Fprintf(app.Out, "Deleted: %s\n", id)
				}
			}
			if app.json {
				return app.printJSON(deleted)
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask for confirmation")
	return cmd
}
