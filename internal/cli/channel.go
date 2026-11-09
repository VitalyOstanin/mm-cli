package cli

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/spf13/cobra"

	"github.com/VitalyOstanin/mm-cli/internal/clierr"
	"github.com/VitalyOstanin/mm-cli/internal/resolve"
)

func newChannelCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "channel",
		Short: "Channels",
		Long: `Create, show, list, edit, archive and mark channels read; show the pinned
messages of a channel. Channel addresses: mm-cli help addressing. Reading
messages is under mm-cli messages and mm-cli post; none of these commands
marks a channel read except channel mark-read.`,
	}
	cmd.AddCommand(newChannelCreateCmd(app), newChannelGetCmd(app), newChannelListCmd(app), newChannelEditCmd(app),
		newChannelStateCmd(app, "archive"), newChannelStateCmd(app, "unarchive"), newChannelReadCmd(app), newPinnedCmd(app))
	return cmd
}

type channelSummary struct {
	ID          string `json:"id"`
	Team        string `json:"team"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Type        string `json:"type"`
	Created     bool   `json:"created"`
}

func newChannelCreateCmd(app *App) *cobra.Command {
	var private, existOK bool
	var display, purpose, header string
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a channel in the team; you become a member",
		Long: `Create an open channel (or a private one with --private) in the current team
(--team or the saved server's team); you become its member. The name is up to 64
lowercase letters, digits, '-' and '_', starting with a letter or digit; the
display name defaults to the name.

--exist-ok makes the command idempotent: if the channel exists, it succeeds
without changes and reports "created": false.

With --json it prints {"id": ..., "team": ..., "name": ..., "display_name": ...,
"type": "O"|"P", "created": true|false}. Exit code 2 for an invalid name or no
team; without --exist-ok an existing name is an error from the server.`,
		Example: `  mm-cli channel create notes --private
  mm-cli channel create notes --private --exist-ok --team myteam
  mm-cli channel create release-notes --purpose "release notes" --json`,
		Args: argsRange(1, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			name := args[0]
			if !resolve.ValidChannelName(name) {
				return clierr.Usage("invalid channel name %q: use up to %d lowercase letters, digits, '-' and '_', starting with a letter or digit", name, model.ChannelNameMaxLength)
			}
			s, err := app.openSession()
			if err != nil {
				return err
			}
			defer s.Close()
			if s.Team == "" {
				return clierr.Usage("no team selected; pass --team")
			}
			report := func(ch *model.Channel, created bool) error {
				out := channelSummary{ID: ch.Id, Team: s.Team, Name: ch.Name, DisplayName: ch.DisplayName, Type: string(ch.Type), Created: created}
				if app.json {
					return app.printJSON(out)
				}
				verb := "Exists"
				if created {
					verb = "Created"
				}
				fmt.Fprintf(app.Out, "%s: %s%s%s %s\n", verb, s.Team, resolve.TeamSeparator, ch.Name, ch.Id)
				return nil
			}
			if existOK {
				ch, resp, err := s.Client.GetChannelByNameForTeamName(ctx, name, s.Team, "")
				if err == nil {
					return report(ch, false)
				}
				if e := clierr.API(resp, err); clierr.ExitCode(e) != clierr.CodeNotFound {
					return e
				}
			}
			team, resp, err := s.Client.GetTeamByName(ctx, s.Team, "")
			if err != nil {
				return clierr.API(resp, err)
			}
			typ := model.ChannelTypeOpen
			if private {
				typ = model.ChannelTypePrivate
			}
			if display == "" {
				display = name
			}
			ch, resp, err := s.Client.CreateChannel(ctx, &model.Channel{
				TeamId: team.Id, Name: name, DisplayName: display, Purpose: purpose, Header: header, Type: typ,
			})
			if err != nil {
				return clierr.API(resp, err)
			}
			return report(ch, true)
		},
	}
	cmd.Flags().BoolVar(&private, "private", false, "create a private channel")
	cmd.Flags().BoolVar(&existOK, "exist-ok", false, "succeed without changes if the channel already exists")
	cmd.Flags().StringVar(&display, "display-name", "", "display name (default: the name)")
	cmd.Flags().StringVar(&purpose, "purpose", "", "channel purpose")
	cmd.Flags().StringVar(&header, "header", "", "channel header")
	return cmd
}

var channelTypeNames = map[model.ChannelType]string{
	model.ChannelTypeOpen:    "open",
	model.ChannelTypePrivate: "private",
	model.ChannelTypeDirect:  "direct",
	model.ChannelTypeGroup:   "group",
}

// directPeer returns the other member of a direct channel (name "id1__id2").
func directPeer(ch *model.Channel, me string) string {
	a, b, ok := strings.Cut(ch.Name, "__")
	if !ok {
		return ""
	}
	if a == me {
		return b
	}
	return a
}

type channelInfo struct {
	ID          string `json:"id"`
	Team        string `json:"team,omitempty"`
	Type        string `json:"type"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Header      string `json:"header,omitempty"`
	Purpose     string `json:"purpose,omitempty"`
	Members     int64  `json:"members,omitempty"`
	Messages    int64  `json:"messages"`
	Unread      int64  `json:"unread,omitempty"`
	Mentions    int64  `json:"mentions,omitempty"`
	Archived    bool   `json:"archived,omitempty"`
}

// displayName shows direct channels as @peer.
func (s *Session) displayName(ctx context.Context, ch *model.Channel) string {
	if ch.Type != model.ChannelTypeDirect {
		return ch.DisplayName
	}
	peer := directPeer(ch, s.Server.UserID)
	if n := s.usernames(ctx, []string{peer})[peer]; n != "" {
		return "@" + n
	}
	return ch.DisplayName
}

func newChannelGetCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "get <channel>",
		Short: "Show a channel: type, names, header, purpose, members, messages",
		Long: channelHelp + `

Shows the channel without reading its messages or marking it read. A direct
channel is shown as @user.

With --json it prints {"id": ..., "type": "open"|"private"|"direct"|"group",
"name": ..., "display_name": ..., "header": ..., "purpose": ...,
"members": N, "messages": N, "archived": true}; empty header, purpose and
false archived are left out. Exit code 5 when the channel does not exist or
is not visible to you.`,
		Example: `  mm-cli channel get town-square
  mm-cli channel get myteam:town-square --json
  mm-cli channel get @bob`,
		Args: argsRange(1, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			s, err := app.openSession()
			if err != nil {
				return err
			}
			defer s.Close()
			var ch *model.Channel
			var stats *model.ChannelStats
			err = s.withChannel(ctx, args[0], func(id string) error {
				var resp *model.Response
				var err error
				if ch, resp, err = s.Client.GetChannel(ctx, id); err != nil {
					return clierr.API(resp, err)
				}
				if stats, resp, err = s.Client.GetChannelStats(ctx, id, "", true); err != nil {
					return clierr.API(resp, err)
				}
				return nil
			})
			if err != nil {
				return err
			}
			info := channelInfo{
				ID: ch.Id, Type: channelTypeNames[ch.Type], Name: ch.Name, DisplayName: s.displayName(ctx, ch),
				Header: ch.Header, Purpose: ch.Purpose, Members: stats.MemberCount, Messages: ch.TotalMsgCount,
				Archived: ch.DeleteAt > 0,
			}
			if app.json {
				return app.printJSON(info)
			}
			fmt.Fprintf(app.Out, "id: %s\ntype: %s\nname: %s\ndisplay name: %s\n", info.ID, info.Type, info.Name, info.DisplayName)
			if info.Header != "" {
				fmt.Fprintf(app.Out, "header: %s\n", info.Header)
			}
			if info.Purpose != "" {
				fmt.Fprintf(app.Out, "purpose: %s\n", info.Purpose)
			}
			fmt.Fprintf(app.Out, "members: %d\nmessages: %d\n", info.Members, info.Messages)
			if info.Archived {
				fmt.Fprintln(app.Out, "archived: yes")
			}
			return nil
		},
	}
}

func newChannelListCmd(app *App) *cobra.Command {
	var unread bool
	var types string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List your channels",
		Long: `List your channels in the selected team (--team or the saved server's team), or
in all your teams when none is selected. --type takes a comma-separated set of
O (open), P (private), D (direct), G (group). --unread keeps channels with
unread messages or mentions. Changes nothing and marks nothing read.

With --json it prints an array of {"id": ..., "team": ..., "type": "open"|
"private"|"direct"|"group", "name": ..., "display_name": ..., "messages": N,
"unread": N, "mentions": N}; unread and mentions are filled only with
--unread (and left out when zero); [] when nothing matches. Direct channels show display_name as @user. Exit code 2 for
an invalid --type.`,
		Example: `  mm-cli channel list
  mm-cli channel list --unread --json
  mm-cli channel list --type D,G --team myteam`,
		Args: argsRange(0, 0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			want, err := parseChannelTypes(types)
			if err != nil {
				return err
			}
			s, err := app.openSession()
			if err != nil {
				return err
			}
			defer s.Close()
			teams, err := listTeams(ctx, s)
			if err != nil {
				return err
			}
			out, chans, err := collectChannels(ctx, s, teams, want, unread)
			if err != nil {
				return err
			}
			applyDirectNames(ctx, s, out, chans)
			sortChannelInfos(out)
			return printChannelList(app, out, unread)
		},
	}
	cmd.Flags().BoolVar(&unread, "unread", false, "only channels with unread messages or mentions")
	cmd.Flags().StringVar(&types, "type", "", "channel types: O,P,D,G")
	return cmd
}

// parseChannelTypes parses the --type value of channel list: a comma-separated
// set of O, P, D, G in any case. An empty value means every type.
func parseChannelTypes(types string) (map[model.ChannelType]bool, error) {
	want := map[model.ChannelType]bool{}
	if types == "" {
		return want, nil
	}
	for _, t := range strings.Split(strings.ToUpper(types), ",") {
		ct := model.ChannelType(strings.TrimSpace(t))
		if _, ok := channelTypeNames[ct]; !ok {
			return nil, clierr.Usage("invalid --type %q: use O, P, D, G", t)
		}
		want[ct] = true
	}
	return want, nil
}

// listTeams returns the selected team, or every team of the user when none
// is selected.
func listTeams(ctx context.Context, s *Session) ([]*model.Team, error) {
	if s.Team != "" {
		t, resp, err := s.Client.GetTeamByName(ctx, s.Team, "")
		if err != nil {
			return nil, clierr.API(resp, err)
		}
		return []*model.Team{t}, nil
	}
	teams, resp, err := s.Client.GetTeamsForUser(ctx, s.Server.UserID, "")
	if err != nil {
		return nil, clierr.API(resp, err)
	}
	return teams, nil
}

// collectChannels returns the user's channels in teams of the wanted types
// (all types when want is empty), each once. With unread, only channels with
// unread messages or mentions are kept and their counts are filled in. The
// channels come back alongside their summaries, in the same order.
func collectChannels(ctx context.Context, s *Session, teams []*model.Team, want map[model.ChannelType]bool, unread bool) ([]channelInfo, []*model.Channel, error) {
	allTeams := s.Team == ""
	list, err := listChannels(ctx, s, teams, allTeams)
	if err != nil {
		return nil, nil, err
	}
	var members map[string]model.ChannelMember
	if unread {
		if members, err = listMembers(ctx, s, teams, allTeams); err != nil {
			return nil, nil, err
		}
	}
	seen := map[string]bool{}
	var out []channelInfo
	var chans []*model.Channel
	for _, tc := range list {
		ch := tc.ch
		if seen[ch.Id] || (len(want) > 0 && !want[ch.Type]) {
			continue
		}
		seen[ch.Id] = true
		info := channelInfo{ID: ch.Id, Type: channelTypeNames[ch.Type], Name: ch.Name, DisplayName: ch.DisplayName, Messages: ch.TotalMsgCount}
		if ch.Type == model.ChannelTypeOpen || ch.Type == model.ChannelTypePrivate {
			info.Team = tc.team
		}
		if unread && !fillUnread(&info, ch, members) {
			continue
		}
		out = append(out, info)
		chans = append(chans, ch)
	}
	return out, chans, nil
}

// fillUnread sets the unread and mention counts of info from the user's
// membership in ch and reports whether the channel has either.
func fillUnread(info *channelInfo, ch *model.Channel, members map[string]model.ChannelMember) bool {
	m, ok := members[ch.Id]
	if !ok {
		return false
	}
	info.Unread = max(ch.TotalMsgCount-m.MsgCount, 0)
	info.Mentions = m.MentionCount
	return info.Unread != 0 || info.Mentions != 0
}

// applyDirectNames shows direct channels in out as @peer, looking up all
// peers in one request; chans[i] is the channel of out[i].
func applyDirectNames(ctx context.Context, s *Session, out []channelInfo, chans []*model.Channel) {
	var peers []string
	for _, ch := range chans {
		if ch.Type == model.ChannelTypeDirect {
			peers = append(peers, directPeer(ch, s.Server.UserID))
		}
	}
	names := s.usernames(ctx, peers)
	for i, ch := range chans {
		if ch.Type == model.ChannelTypeDirect {
			if n := names[directPeer(ch, s.Server.UserID)]; n != "" {
				out[i].DisplayName = "@" + n
			}
		}
	}
}

// sortChannelInfos orders channels by type name, descending, then by display
// name ignoring case.
func sortChannelInfos(out []channelInfo) {
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Type != out[j].Type {
			return out[i].Type > out[j].Type
		}
		return strings.ToLower(out[i].DisplayName) < strings.ToLower(out[j].DisplayName)
	})
}

// printChannelList prints channels as JSON or one line each; with unread the
// lines carry the unread and mention counts.
func printChannelList(app *App, out []channelInfo, unread bool) error {
	if app.json {
		if out == nil {
			out = []channelInfo{}
		}
		return app.printJSON(out)
	}
	for _, c := range out {
		addr := c.Name
		if c.Team != "" {
			addr = c.Team + resolve.TeamSeparator + c.Name
		}
		if c.Type == channelTypeNames[model.ChannelTypeDirect] || c.Type == channelTypeNames[model.ChannelTypeGroup] {
			addr = c.ID
		}
		fmt.Fprintf(app.Out, "%-8s %s  %s", c.Type, c.DisplayName, addr)
		if unread {
			fmt.Fprintf(app.Out, "  unread %d", c.Unread)
			if c.Mentions > 0 {
				fmt.Fprintf(app.Out, ", mentions %d", c.Mentions)
			}
		}
		fmt.Fprintln(app.Out)
	}
	return nil
}

// teamChannel is a channel with the name of the team it belongs to; direct
// and group channels have none.
type teamChannel struct {
	ch   *model.Channel
	team string
}

// membersPerPage is the page size for the user's channel memberships, the
// largest the server accepts.
const membersPerPage = 200

// listChannels returns the user's channels in teams. With allTeams, one
// request covers every team, so direct and group channels do not come again
// with each team; channels of teams outside teams are left out.
func listChannels(ctx context.Context, s *Session, teams []*model.Team, allTeams bool) ([]teamChannel, error) {
	names := make(map[string]string, len(teams))
	for _, t := range teams {
		names[t.Id] = t.Name
	}
	var out []teamChannel
	if allTeams {
		list, resp, err := s.Client.GetChannelsForUserWithLastDeleteAt(ctx, s.Server.UserID, 0)
		if err != nil {
			return nil, clierr.API(resp, err)
		}
		for _, ch := range list {
			team, ok := names[ch.TeamId]
			if ch.TeamId != "" && !ok {
				continue
			}
			out = append(out, teamChannel{ch: ch, team: team})
		}
		return out, nil
	}
	for _, t := range teams {
		list, resp, err := s.Client.GetChannelsForTeamForUser(ctx, t.Id, s.Server.UserID, false, "")
		if err != nil {
			return nil, clierr.API(resp, err)
		}
		for _, ch := range list {
			out = append(out, teamChannel{ch: ch, team: t.Name})
		}
	}
	return out, nil
}

// listMembers returns the user's channel memberships by channel id. With
// allTeams they come for every team at once, a page at a time.
func listMembers(ctx context.Context, s *Session, teams []*model.Team, allTeams bool) (map[string]model.ChannelMember, error) {
	members := map[string]model.ChannelMember{}
	if allTeams {
		for page := 0; ; page++ {
			ms, resp, err := s.Client.GetChannelMembersWithTeamData(ctx, s.Server.UserID, page, membersPerPage)
			if err != nil {
				return nil, clierr.API(resp, err)
			}
			for _, m := range ms {
				members[m.ChannelId] = m.ChannelMember
			}
			if len(ms) < membersPerPage {
				return members, nil
			}
		}
	}
	for _, t := range teams {
		ms, resp, err := s.Client.GetChannelMembersForUser(ctx, s.Server.UserID, t.Id, "")
		if err != nil {
			return nil, clierr.API(resp, err)
		}
		for _, m := range ms {
			members[m.ChannelId] = m
		}
	}
	return members, nil
}

func newChannelEditCmd(app *App) *cobra.Command {
	var display, purpose, header string
	cmd := &cobra.Command{
		Use:   "edit <channel> [--display-name <name>] [--purpose <text>] [--header <text>]",
		Short: "Change the display name, purpose or header",
		Long: channelHelp + `

Changes only the fields passed; an empty value clears a field (--header "").
The channel name (its address) does not change.

With --json it prints the channel as channel get does, without members.
Exit code 2 when no field is passed, 4 without the permission to edit.`,
		Example: `  mm-cli channel edit notes --header "Daily notes"
  mm-cli channel edit notes --display-name Notes --purpose "" --json`,
		Args: argsRange(1, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			patch := &model.ChannelPatch{}
			if cmd.Flags().Changed("display-name") {
				patch.DisplayName = &display
			}
			if cmd.Flags().Changed("purpose") {
				patch.Purpose = &purpose
			}
			if cmd.Flags().Changed("header") {
				patch.Header = &header
			}
			if patch.DisplayName == nil && patch.Purpose == nil && patch.Header == nil {
				return clierr.Usage("nothing to change: pass --display-name, --purpose or --header")
			}
			s, err := app.openSession()
			if err != nil {
				return err
			}
			defer s.Close()
			return s.withChannel(ctx, args[0], func(id string) error {
				ch, resp, err := s.Client.PatchChannel(ctx, id, patch)
				if err != nil {
					return clierr.API(resp, err)
				}
				if app.json {
					return app.printJSON(channelInfo{ID: ch.Id, Type: channelTypeNames[ch.Type], Name: ch.Name, DisplayName: ch.DisplayName, Header: ch.Header, Purpose: ch.Purpose, Messages: ch.TotalMsgCount})
				}
				fmt.Fprintf(app.Out, "Edited: %s\n", ch.Id)
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&display, "display-name", "", "new display name")
	cmd.Flags().StringVar(&purpose, "purpose", "", "new purpose")
	cmd.Flags().StringVar(&header, "header", "", "new header")
	return cmd
}

func newChannelStateCmd(app *App, action string) *cobra.Command {
	t := channelStateActions[action]
	return &cobra.Command{
		Use:     action + " <channel>",
		Short:   t.short,
		Long:    t.long,
		Example: t.example,
		Args:    argsRange(1, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			s, err := app.openSession()
			if err != nil {
				return err
			}
			defer s.Close()
			return s.withChannel(ctx, args[0], func(id string) error {
				return t.run(ctx, app, s, id)
			})
		},
	}
}

var channelStateActions = map[string]toggleAction{
	"archive": {short: "Archive a channel (reversible with unarchive)", verb: "Archived", done: "archived",
		long: channelHelp + `

Archives the channel for everyone: it becomes read-only and leaves the
channel lists. Reversible with mm-cli channel unarchive; asks no confirmation.

With --json it prints {"action": "archived", "id": "<channel id>"}. Exit code 4
without the permission, 5 when the channel does not exist.`,
		example: `  mm-cli channel archive old-project
  mm-cli channel archive myteam:old-project --json`,
		call: func(ctx context.Context, s *Session, id string) (*model.Response, error) {
			return s.Client.DeleteChannel(ctx, id)
		}},
	"unarchive": {short: "Restore an archived channel", verb: "Unarchived", done: "unarchived",
		long: `Restore an archived channel, so that it can be written to again. Address it by
name (team:name for another team) or id; mm-cli help addressing.

With --json it prints {"action": "unarchived", "id": "<channel id>"}. Exit code
4 without the permission, 5 when the channel does not exist.`,
		example: `  mm-cli channel unarchive old-project
  mm-cli channel unarchive <channel-id> --json`,
		call: func(ctx context.Context, s *Session, id string) (*model.Response, error) {
			_, resp, err := s.Client.RestoreChannel(ctx, id)
			return resp, err
		}},
}

func newChannelReadCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "mark-read <channel>",
		Short: "Mark a channel as read",
		Long: channelHelp + `

Mark a channel as read, as opening it in the app does: its unread count and
mentions drop to zero. Reading messages with mm-cli never does this by itself.

With --json it prints {"action": "marked_read", "id": "<channel id>"}.`,
		Example: `  mm-cli channel mark-read town-square
  mm-cli channel mark-read @bob --json`,
		Args: argsRange(1, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			s, err := app.openSession()
			if err != nil {
				return err
			}
			defer s.Close()
			return s.withChannel(ctx, args[0], func(id string) error {
				if _, resp, err := s.Client.ViewChannel(ctx, s.Server.UserID, &model.ChannelView{ChannelId: id}); err != nil {
					return clierr.API(resp, err)
				}
				return app.printDone(actionResult{Action: "marked_read", ID: id}, "Marked as read: %s", id)
			})
		},
	}
}
