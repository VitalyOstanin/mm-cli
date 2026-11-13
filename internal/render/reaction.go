package render

import (
	"cmp"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

// Reaction is the stable JSON shape of a reaction put on a message, with the
// message it belongs to; RootID is the root of that message's thread, empty for
// a root post. CreateAt is the time of the reaction, not of the
// message.
type Reaction struct {
	PostID    string `json:"post_id"`
	RootID    string `json:"root_id"`
	ChannelID string `json:"channel_id"`
	Channel   string `json:"channel"`
	// ChannelDisplayName is the name the app shows for the channel.
	ChannelDisplayName string `json:"channel_display_name"`
	Team               string `json:"team"`
	UserID             string `json:"user_id"`
	User               string `json:"user"`
	Emoji              string `json:"emoji"`
	CreateAt           string `json:"create_at"`
	Message            string `json:"message"`
	Permalink          string `json:"permalink"`
	AppLink            string `json:"app_link"`
	// ChannelLabel is what the text output shows for the channel instead of
	// the address when it is set: a group channel's address is an id.
	ChannelLabel string `json:"-"`
}

// ReactionFromModel builds the record of reaction r on post p. names maps user
// ids to usernames; channel and team are the names shown for the message's
// channel and used in its links, display and label the display name and the
// text label of the channel; serverURL is the server's base URL.
func ReactionFromModel(r *model.Reaction, p *model.Post, names map[string]string, channel, display, label, team, serverURL string) Reaction {
	permalink, appLink := PostLinks(serverURL, team, p.Id)
	return Reaction{
		PostID:             p.Id,
		RootID:             p.RootId,
		ChannelID:          p.ChannelId,
		Channel:            channel,
		ChannelDisplayName: display,
		ChannelLabel:       label,
		Team:               team,
		UserID:             r.UserId,
		User:               names[r.UserId],
		Emoji:              r.EmojiName,
		CreateAt:           RFC3339(r.CreateAt),
		Message:            p.Message,
		Permalink:          permalink,
		AppLink:            appLink,
	}
}

// PostLinks returns the web link of a message, as the app's Copy Link gives
// it, and the mattermost:// link that opens the same message in the Desktop
// app. Both keep any path prefix of the server URL; the app link has no scheme
// of the server in it. Without a parsable server URL both are empty.
func PostLinks(serverURL, team, postID string) (permalink, appLink string) {
	u, err := url.Parse(strings.TrimRight(serverURL, "/"))
	if err != nil || u.Host == "" {
		return "", ""
	}
	// Without a team the app's own redirect route picks one (the webapp has
	// /_redirect/pl/:postid), so the link never has an empty segment.
	if team == "" {
		team = "_redirect"
	}
	path := u.EscapedPath() + "/" + url.PathEscape(team) + "/pl/" + postID
	return u.Scheme + "://" + u.Host + path, "mattermost://" + u.Host + path
}

// ReactionsText prints reactions for people, one block each: time, user,
// emoji and channel on the first line, the message shortened to truncate
// characters (all of it when truncate is 0), the app link last.
func ReactionsText(w io.Writer, rs []Reaction, loc *time.Location, truncate int) error {
	var b strings.Builder
	for i, r := range rs {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "%s  @%s  %s  %s\n", LocalTime(r.CreateAt, loc), cmp.Or(r.User, r.UserID), EmojiLabel(r.Emoji), cmp.Or(r.ChannelLabel, r.Channel, r.ChannelID))
		// A message line stays one line: the block is easy to scan and to
		// split by blank lines.
		if msg := Snippet(r.Message, truncate); msg != "" {
			b.WriteString(msg)
			b.WriteString("\n")
		}
		if r.AppLink != "" {
			b.WriteString(r.AppLink)
			b.WriteString("\n")
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// Snippet is the start of a message for a notification: one line, cut to n
// characters.
func Snippet(msg string, n int) string {
	return shorten(strings.Join(strings.Fields(msg), " "), n)
}
