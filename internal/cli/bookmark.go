package cli

import (
	"fmt"
	"net/http"
	"os"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/spf13/cobra"

	"github.com/VitalyOstanin/mm-cli/internal/clierr"
)

func newBookmarkCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{Use: "bookmark", Short: "Channel bookmarks (links and files under the channel header)"}
	cmd.AddCommand(newBookmarkAddCmd(app), newBookmarkGetCmd(app), newBookmarkListCmd(app), newBookmarkEditCmd(app), newBookmarkDeleteCmd(app))
	return cmd
}

type bookmarkInfo struct {
	ID        string `json:"id"`
	ChannelID string `json:"channel_id"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	URL       string `json:"url,omitempty"`
	FileID    string `json:"file_id,omitempty"`
	FileName  string `json:"file_name,omitempty"`
	Emoji     string `json:"emoji,omitempty"`
	SortOrder int64  `json:"sort_order"`
}

func toBookmark(b *model.ChannelBookmarkWithFileInfo) bookmarkInfo {
	out := bookmarkInfo{}
	if b == nil || b.ChannelBookmark == nil {
		return out
	}
	out = bookmarkInfo{ID: b.Id, ChannelID: b.ChannelId, Name: b.DisplayName, Type: string(b.Type),
		URL: b.LinkUrl, FileID: b.FileId, Emoji: b.Emoji, SortOrder: b.SortOrder}
	if b.FileInfo != nil {
		out.FileName = b.FileInfo.Name
	}
	return out
}

// bookmarkErr turns the answers of servers without bookmarks into code 5.
func bookmarkErr(resp *model.Response, err error) error {
	if resp != nil && (resp.StatusCode == http.StatusNotImplemented || resp.StatusCode == http.StatusNotFound) {
		return clierr.Wrap(clierr.CodeNotFound, err, "not found, or channel bookmarks are not supported by this server")
	}
	return clierr.API(resp, err)
}

func (a *App) printBookmarks(list []bookmarkInfo) error {
	if a.json {
		return a.printJSON(list)
	}
	for _, b := range list {
		target := b.URL
		if b.Type == string(model.ChannelBookmarkFile) {
			target = "file " + b.FileName + " " + b.FileID
		}
		emoji := ""
		if b.Emoji != "" {
			emoji = "  :" + b.Emoji + ":"
		}
		fmt.Fprintf(a.Out, "%s  %s  %s%s\n", b.ID, b.Name, target, emoji)
	}
	return nil
}

func newBookmarkAddCmd(app *App) *cobra.Command {
	var link, file, name, emoji string
	cmd := &cobra.Command{
		Use:   "add <channel> (--url <url> | -f <file>) --name <name> [--emoji <name>]",
		Short: "Add a link or file bookmark to a channel",
		Args:  argsRange(1, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if (link == "") == (file == "") {
				return clierr.Usage("pass exactly one of --url and -f")
			}
			if name == "" {
				return clierr.Usage("--name is required")
			}
			var data []byte
			if file != "" {
				var err error
				if data, err = os.ReadFile(file); err != nil {
					return clierr.Usage("%v", err)
				}
			}
			s, err := app.openSession()
			if err != nil {
				return err
			}
			defer s.Close()
			return s.withChannel(ctx, args[0], func(id string) error {
				b := &model.ChannelBookmark{ChannelId: id, DisplayName: name, Emoji: emoji, Type: model.ChannelBookmarkLink, LinkUrl: link}
				if file != "" {
					fileID, err := s.uploadOne(ctx, data, id, file, "")
					if err != nil {
						return err
					}
					b.Type, b.FileId, b.LinkUrl = model.ChannelBookmarkFile, fileID, ""
				}
				created, resp, err := s.Client.CreateChannelBookmark(ctx, b)
				if err != nil {
					return bookmarkErr(resp, err)
				}
				if app.json {
					return app.printJSON(toBookmark(created))
				}
				fmt.Fprintf(app.Out, "Added: %s\n", created.Id)
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&link, "url", "", "link target")
	cmd.Flags().StringVarP(&file, "file", "f", "", "file to upload as the bookmark")
	cmd.Flags().StringVar(&name, "name", "", "bookmark title")
	cmd.Flags().StringVar(&emoji, "emoji", "", "emoji name, without colons")
	return cmd
}

func (s *Session) listBookmarks(cmd *cobra.Command, channel string) ([]bookmarkInfo, error) {
	ctx := cmd.Context()
	var out []bookmarkInfo
	err := s.withChannel(ctx, channel, func(id string) error {
		list, resp, err := s.Client.ListChannelBookmarksForChannel(ctx, id, 0)
		if err != nil {
			return bookmarkErr(resp, err)
		}
		out = make([]bookmarkInfo, 0, len(list))
		for _, b := range list {
			if b.ChannelBookmark != nil && b.DeleteAt == 0 {
				out = append(out, toBookmark(b))
			}
		}
		return nil
	})
	return out, err
}

func newBookmarkListCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "list <channel>",
		Short: "List the bookmarks of a channel",
		Args:  argsRange(1, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := app.openSession()
			if err != nil {
				return err
			}
			defer s.Close()
			list, err := s.listBookmarks(cmd, args[0])
			if err != nil {
				return err
			}
			return app.printBookmarks(list)
		},
	}
}

func newBookmarkGetCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "get <channel> <bookmark-id>",
		Short: "Show one bookmark",
		Args:  argsRange(2, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validID(args[1], "bookmark id"); err != nil {
				return err
			}
			s, err := app.openSession()
			if err != nil {
				return err
			}
			defer s.Close()
			list, err := s.listBookmarks(cmd, args[0])
			if err != nil {
				return err
			}
			for _, b := range list {
				if b.ID == args[1] {
					if app.json {
						return app.printJSON(b)
					}
					return app.printBookmarks([]bookmarkInfo{b})
				}
			}
			return clierr.New(clierr.CodeNotFound, "bookmark %s not found in the channel", args[1])
		},
	}
}

func newBookmarkEditCmd(app *App) *cobra.Command {
	var name, link, emoji string
	cmd := &cobra.Command{
		Use:   "edit <channel> <bookmark-id> [--name <name>] [--url <url>] [--emoji <name>]",
		Short: "Change a bookmark",
		Args:  argsRange(2, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if err := validID(args[1], "bookmark id"); err != nil {
				return err
			}
			patch := &model.ChannelBookmarkPatch{}
			if cmd.Flags().Changed("name") {
				patch.DisplayName = &name
			}
			if cmd.Flags().Changed("url") {
				patch.LinkUrl = &link
			}
			if cmd.Flags().Changed("emoji") {
				patch.Emoji = &emoji
			}
			if patch.DisplayName == nil && patch.LinkUrl == nil && patch.Emoji == nil {
				return clierr.Usage("nothing to change: pass --name, --url or --emoji")
			}
			s, err := app.openSession()
			if err != nil {
				return err
			}
			defer s.Close()
			return s.withChannel(ctx, args[0], func(id string) error {
				res, resp, err := s.Client.UpdateChannelBookmark(ctx, id, args[1], patch)
				if err != nil {
					return bookmarkErr(resp, err)
				}
				if app.json {
					return app.printJSON(toBookmark(res.Updated))
				}
				fmt.Fprintf(app.Out, "Edited: %s\n", args[1])
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "new title")
	cmd.Flags().StringVar(&link, "url", "", "new link target")
	cmd.Flags().StringVar(&emoji, "emoji", "", "new emoji name")
	return cmd
}

func newBookmarkDeleteCmd(app *App) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <channel> <bookmark-id>",
		Short: "Delete a bookmark",
		Args:  argsRange(2, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if err := validID(args[1], "bookmark id"); err != nil {
				return err
			}
			if err := app.confirm(cmd.Context(), fmt.Sprintf("Delete bookmark %s?", args[1]), yes); err != nil {
				return err
			}
			s, err := app.openSession()
			if err != nil {
				return err
			}
			defer s.Close()
			return s.withChannel(ctx, args[0], func(id string) error {
				if _, resp, err := s.Client.DeleteChannelBookmark(ctx, id, args[1]); err != nil {
					return bookmarkErr(resp, err)
				}
				return app.printDone(actionResult{Action: "deleted", ID: args[1], ChannelID: id}, "Deleted: %s", args[1])
			})
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask for confirmation")
	return cmd
}
