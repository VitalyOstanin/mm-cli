package cli

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/spf13/cobra"

	"github.com/VitalyOstanin/mm-cli/internal/clierr"
)

// emojiNameRe is the server's rule for the name of a reaction (see
// model.Reaction.IsValid), checked here so that a typo is exit code 2 and
// costs no request.
var emojiNameRe = regexp.MustCompile(`^[a-z0-9\-+_]+$`)

// emojiName turns an argument such as +1, :+1: or Thumbs_Up into the name the
// server stores: surrounding colons removed and lower-cased, as the server
// lower-cases the name itself.
func emojiName(arg string) (string, error) {
	name := strings.ToLower(strings.TrimSuffix(strings.TrimPrefix(arg, ":"), ":"))
	if name == "" || len(name) > model.EmojiNameMaxLength || !emojiNameRe.MatchString(name) {
		return "", clierr.Usage("invalid emoji name %q: use letters, digits, - + _ (at most %d characters), with or without colons", arg, model.EmojiNameMaxLength)
	}
	return name, nil
}

// emojiNames converts all arguments and drops repeats, keeping the order, so
// the same reaction is never requested twice.
func emojiNames(args []string) ([]string, error) {
	var out []string
	for _, a := range args {
		n, err := emojiName(a)
		if err != nil {
			return nil, err
		}
		if !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	return out, nil
}

// newReactionCmd returns the post reaction group: add, remove and list.
func newReactionCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reaction",
		Short: "Add, remove and list reactions on messages",
		Long: `Reactions (emoji) on messages, as "Add Reaction" and clicking a reaction
to remove it work in the app.

  add     put your reaction on a message (--replace swaps one of yours)
  remove  take your reaction off a message
  list    all reactions of one message, or the history of reactions
          other people put on your messages

The reactions of a message also appear in the "reactions" field of messages,
thread and post output (emoji, count, users).`,
	}
	cmd.AddCommand(newReactionAddCmd(app), newReactionRemoveCmd(app), newReactionsCmd(app))
	return cmd
}

// newReactionAddCmd returns post reaction add, after adding a reaction in
// the app's message menu.
func newReactionAddCmd(app *App) *cobra.Command {
	var replace string
	cmd := &cobra.Command{
		Use:         "add <post-id> <emoji>...",
		Annotations: effect(effectServer),
		Short:       "Add your reaction to a message",
		Long: `Add your reaction (emoji) to a message, as the emoji picker of the app does;
everyone in the channel sees it. An emoji is a name such as +1, :+1:,
white_check_mark or the name of a custom emoji; colons around it are
optional and capital letters are lower-cased, as the server does. A name the
server's rule does not allow (letters, digits, - + _, at most 64 characters)
is exit code 2 before any request. One request per emoji.

Adding a reaction you already put on the message is not an error: the server
accepts it and nothing changes, so the command can be repeated safely and
the result looks the same.

--replace <old-emoji> swaps one of your reactions for another, which is the
nearest thing to editing a reaction (the server has none): give exactly one
new emoji. The new reaction is added first and the old one removed after
that, so a failure never leaves the message without a reaction of yours. If
the new one cannot be added nothing changes; if removing the old one fails,
the error says that the new reaction was added. A replaced reaction that is
not there is not an error, as for post reaction remove. The old and new
emoji must differ.

Reactions are listed with post reaction list <post-id> (all of one message)
or post reaction list (history on your messages), removed with post reaction
remove; the
"reactions" field of messages and thread output shows them too.

Text output prints one line per emoji as it is done. With --json it prints
an array with one object per emoji: {"action": "reaction_added", "post_id":
"<post id>", "emoji": "<name>"}; with --replace one object {"action":
"reaction_replaced", "post_id": ..., "emoji": <new>, "old_emoji": <old>}.
When a request fails the array of what was already done is printed first,
then the error names the emoji. Exit code 2 for an invalid post id or emoji,
or --replace without exactly one emoji; 3 when not signed in; 4 without
access (or reactions disabled); 5 when the message or a custom emoji does
not exist.`,
		Example: `  mm-cli post reaction add <post-id> +1
  mm-cli post reaction add <post-id> :white_check_mark: eyes --json
  mm-cli post reaction add <post-id> heart --replace +1`,
		Args: argsRange(2, unlimited),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validID(args[0], "post id"); err != nil {
				return err
			}
			names, err := emojiNames(args[1:])
			if err != nil {
				return err
			}
			old := ""
			if cmd.Flags().Changed("replace") {
				if len(names) != 1 {
					return clierr.Usage("--replace needs exactly one new emoji, got %d", len(names))
				}
				if old, err = emojiName(replace); err != nil {
					return err
				}
				if old == names[0] {
					return clierr.Usage("--replace: the old and the new emoji are the same")
				}
			}
			s, err := app.openSession()
			if err != nil {
				return err
			}
			defer s.Close()
			return runReact(cmd.Context(), app, s, args[0], names, old)
		},
	}
	cmd.Flags().StringVar(&replace, "replace", "", "remove this reaction of yours after adding the new one")
	return cmd
}

// runReact adds the reactions, then removes old when replacing.
func runReact(ctx context.Context, app *App, s *Session, post string, names []string, old string) error {
	var done doneList
	for _, name := range names {
		r := &model.Reaction{UserId: s.Server.UserID, PostId: post, EmojiName: name}
		if _, resp, err := s.Client.SaveReaction(ctx, r); err != nil {
			return done.fail(app, "react :"+name+":", clierr.API(resp, err))
		}
		if old != "" {
			// Added; shown as "added" only if the removal below fails.
			done = append(done, actionResult{Action: "reaction_added", PostID: post, Emoji: name})
			resp, err := s.Client.DeleteReaction(ctx, &model.Reaction{UserId: s.Server.UserID, PostId: post, EmojiName: old})
			if err != nil {
				return done.fail(app, fmt.Sprintf("replace :%s: with :%s: (the new reaction was added, :%s: was not removed)", old, name, old), clierr.API(resp, err))
			}
			done[len(done)-1] = actionResult{Action: "reaction_replaced", PostID: post, Emoji: name, OldEmoji: old}
			if !app.json {
				fmt.Fprintf(app.Out, "Replaced :%s: with :%s: on %s\n", old, name, post)
			}
			continue
		}
		done = append(done, actionResult{Action: "reaction_added", PostID: post, Emoji: name})
		if !app.json {
			fmt.Fprintf(app.Out, "Reacted :%s: on %s\n", name, post)
		}
	}
	return done.finish(app)
}

// runUnreact removes the reactions, one request per emoji.
func runUnreact(ctx context.Context, app *App, s *Session, post string, names []string) error {
	var done doneList
	for _, name := range names {
		r := &model.Reaction{UserId: s.Server.UserID, PostId: post, EmojiName: name}
		if resp, err := s.Client.DeleteReaction(ctx, r); err != nil {
			return done.fail(app, "unreact :"+name+":", clierr.API(resp, err))
		}
		done = append(done, actionResult{Action: "reaction_removed", PostID: post, Emoji: name})
		if !app.json {
			fmt.Fprintf(app.Out, "Removed :%s: from %s\n", name, post)
		}
	}
	return done.finish(app)
}

// newReactionRemoveCmd returns post reaction remove, after removing a
// reaction in the app.
func newReactionRemoveCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:         "remove <post-id> <emoji>...",
		Annotations: effect(effectServer),
		Short:       "Remove your reaction from a message",
		Long: `Remove your reaction (emoji) from a message; other people's reactions are not
touched. An emoji is written as for post reaction add: +1, :+1: or a custom
name.
An invalid name is exit code 2 before any request. One request per emoji.

Removing a reaction that is not there is not an error: the server answers
success and nothing changes, so the command can be repeated safely.

Text output prints one line per emoji as it is done. With --json it prints
an array with one object per emoji: {"action": "reaction_removed",
"post_id": "<post id>", "emoji": "<name>"}. When a request fails the array of
what was already removed is printed first, then the error names the emoji.
Exit code 2 for an invalid post id or emoji, 3 when not signed in, 4 without
access, 5 when the message does not exist.`,
		Example: `  mm-cli post reaction remove <post-id> +1
  mm-cli post reaction remove <post-id> :eyes: heart --json`,
		Args: argsRange(2, unlimited),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validID(args[0], "post id"); err != nil {
				return err
			}
			names, err := emojiNames(args[1:])
			if err != nil {
				return err
			}
			s, err := app.openSession()
			if err != nil {
				return err
			}
			defer s.Close()
			return runUnreact(cmd.Context(), app, s, args[0], names)
		},
	}
}
