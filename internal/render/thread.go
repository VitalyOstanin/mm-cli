package render

import (
	"cmp"
	"fmt"
	"io"
	"strings"
	"time"
)

// Thread is one followed thread in the output of thread list. The JSON field
// names are part of the public interface.
type Thread struct {
	ID             string `json:"id"`
	Team           string `json:"team"`
	ChannelID      string `json:"channel_id"`
	Channel        string `json:"channel"`
	ChannelType    string `json:"channel_type"`
	Root           Post   `json:"root"`
	ReplyCount     int64  `json:"reply_count"`
	UnreadReplies  int64  `json:"unread_replies"`
	UnreadMentions int64  `json:"unread_mentions"`
	// LastReplyAt is RFC 3339, empty when the thread has no replies yet.
	LastReplyAt string `json:"last_reply_at"`
	// Label is the channel as text output shows it: #name, @user or group.
	Label string `json:"-"`
}

// ThreadsText prints threads for people, in the given time zone. The time is
// that of the last reply, or of the root post when there are no replies.
// truncate > 0 shortens the root post to that many characters.
func ThreadsText(w io.Writer, threads []Thread, loc *time.Location, truncate int) error {
	var b strings.Builder
	for i, t := range threads {
		if i > 0 {
			b.WriteString("\n")
		}
		ts := LocalTime(cmp.Or(t.LastReplyAt, t.Root.CreateAt), loc)
		who := cmp.Or(t.Root.User.Username, t.Root.User.ID)
		fmt.Fprintf(&b, "%s  %s  @%s  id %s\n", ts, t.Label, who, t.ID)
		fmt.Fprintf(&b, "replies %d, unread %d, mentions %d\n", t.ReplyCount, t.UnreadReplies, t.UnreadMentions)
		if msg := shorten(t.Root.Message, truncate); msg != "" {
			b.WriteString(msg)
			b.WriteString("\n")
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}
