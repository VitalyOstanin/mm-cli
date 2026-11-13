// Package render prints posts as text or JSON. The JSON field names are a
// public interface.
package render

import (
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/mattermost/mattermost/server/public/model"
)

// User is the author of a post.
type User struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

// File is an attachment.
type File struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Size int64  `json:"size"`
	Mime string `json:"mime"`
}

// Post is the stable JSON shape of a post.
type Post struct {
	ID         string `json:"id"`
	ChannelID  string `json:"channel_id"`
	RootID     string `json:"root_id"`
	CreateAt   string `json:"create_at"`
	User       User   `json:"user"`
	Message    string `json:"message"`
	Files      []File `json:"files"`
	ReplyCount int64  `json:"reply_count"`
	Edited     bool   `json:"edited"`
	Pinned     bool   `json:"pinned"`
	// Reactions are grouped by emoji in the order the emoji was first used;
	// omitted when nobody reacted.
	Reactions []ReactionGroup `json:"reactions,omitempty"`
}

// ReactionGroup is the reactions of one emoji on a message: how many and who.
type ReactionGroup struct {
	Emoji string   `json:"emoji"`
	Count int      `json:"count"`
	Users []string `json:"users"`
}

// FromModel converts a post; names maps user ids to usernames.
func FromModel(p *model.Post, names map[string]string) Post {
	out := Post{
		ID:         p.Id,
		ChannelID:  p.ChannelId,
		RootID:     p.RootId,
		CreateAt:   time.UnixMilli(p.CreateAt).UTC().Format(time.RFC3339),
		User:       User{ID: p.UserId, Username: names[p.UserId]},
		Message:    p.Message,
		ReplyCount: p.ReplyCount,
		Edited:     p.EditAt > 0,
		Pinned:     p.IsPinned,
	}
	out.Files = filesOf(p.Metadata, p.FileIds)
	out.Reactions = groupReactions(p.Metadata, names)
	return out
}

// ReactorIDs returns the ids of the users who reacted to p, so that callers
// can look their names up together with the authors.
func ReactorIDs(p *model.Post) []string {
	if p.Metadata == nil {
		return nil
	}
	var ids []string
	for _, r := range p.Metadata.Reactions {
		if r != nil {
			ids = append(ids, r.UserId)
		}
	}
	return ids
}

// UserIDs returns the ids whose usernames are needed to render p: its author
// first, then the users who reacted to it. Duplicates are kept; the lookup
// that consumes them deduplicates.
func UserIDs(p *model.Post) []string {
	return append([]string{p.UserId}, ReactorIDs(p)...)
}

// groupReactions groups the reactions the server put in the metadata by emoji,
// in the order of first use (earliest reaction of each emoji), users in the
// order they reacted. A user whose name is unknown is shown by id.
func groupReactions(meta *model.PostMetadata, names map[string]string) []ReactionGroup {
	if meta == nil || len(meta.Reactions) == 0 {
		return nil
	}
	rs := make([]*model.Reaction, 0, len(meta.Reactions))
	for _, r := range meta.Reactions {
		if r != nil {
			rs = append(rs, r)
		}
	}
	slices.SortStableFunc(rs, func(a, b *model.Reaction) int {
		return cmp.Or(cmp.Compare(a.CreateAt, b.CreateAt), strings.Compare(a.UserId, b.UserId))
	})
	var out []ReactionGroup
	idx := map[string]int{}
	for _, r := range rs {
		i, ok := idx[r.EmojiName]
		if !ok {
			i = len(out)
			idx[r.EmojiName] = i
			out = append(out, ReactionGroup{Emoji: r.EmojiName, Users: []string{}})
		}
		out[i].Count++
		out[i].Users = append(out[i].Users, cmp.Or(names[r.UserId], r.UserId))
	}
	return out
}

// Sorted returns the live posts of a list, oldest first. When the list has
// an order, only the posts named in it are returned: the server also puts the
// root posts of threads into Posts, and those are not part of the answer. An
// order that names none of the posts is ignored.
func Sorted(pl *model.PostList) []*model.Post {
	if pl == nil {
		return nil
	}
	out := make([]*model.Post, 0, len(pl.Posts))
	seen := make(map[string]bool, len(pl.Order))
	for _, id := range pl.Order {
		p := pl.Posts[id]
		if p == nil || seen[id] {
			continue
		}
		seen[id] = true
		if p.DeleteAt == 0 {
			out = append(out, p)
		}
	}
	if len(seen) == 0 {
		for _, p := range pl.Posts {
			if p.DeleteAt == 0 {
				out = append(out, p)
			}
		}
	}
	slices.SortFunc(out, func(a, b *model.Post) int {
		return cmp.Or(cmp.Compare(a.CreateAt, b.CreateAt), strings.Compare(a.Id, b.Id))
	})
	return out
}

// PostsText prints posts for people, in the given time zone. truncate > 0
// shortens long messages to that many characters.
func PostsText(w io.Writer, posts []Post, loc *time.Location, truncate int) error {
	var b strings.Builder
	for i, p := range posts {
		if i > 0 {
			b.WriteString("\n")
		}
		ts := LocalTime(p.CreateAt, loc)
		who := cmp.Or(p.User.Username, p.User.ID)
		fmt.Fprintf(&b, "%s  @%s  id %s", ts, who, p.ID)
		if p.RootID != "" {
			fmt.Fprintf(&b, ", reply to %s", p.RootID)
		}
		if p.ReplyCount > 0 {
			fmt.Fprintf(&b, ", replies %d", p.ReplyCount)
		}
		if p.Edited {
			b.WriteString(", edited")
		}
		if p.Pinned {
			b.WriteString(", pinned")
		}
		b.WriteString("\n")
		if msg := shorten(p.Message, truncate); msg != "" {
			b.WriteString(msg)
			b.WriteString("\n")
		}
		writeFiles(&b, p.Files)
		if len(p.Reactions) > 0 {
			for _, g := range p.Reactions {
				fmt.Fprintf(&b, "  %s %d", EmojiLabel(g.Emoji), g.Count)
			}
			b.WriteString("\n")
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// shorten cuts s to n user-perceived characters (grapheme clusters).
func shorten(s string, n int) string {
	if n <= 0 {
		return s
	}
	count, prev, joined, ri := 0, rune(-1), false, 0
	for i, r := range s {
		if prev >= 0 && (joined || extends(r) || (isRegional(r) && ri%2 == 1)) {
			joined = r == zwj
			if isRegional(r) {
				ri++
			}
			prev = r
			continue
		}
		if count == n {
			return s[:i] + "…"
		}
		count++
		joined = r == zwj
		ri = 0
		if isRegional(r) {
			ri = 1
		}
		prev = r
	}
	return s
}

const zwj = '\u200d'

// extends reports whether r continues the grapheme cluster before it. It
// covers the common cases of Unicode text segmentation (UAX #29): combining
// marks, variation selectors, emoji modifiers, tags, zero-width joiners and
// Hangul vowel and final jamo.
func extends(r rune) bool {
	switch {
	case r == zwj:
		return true
	case r >= 0x1f3fb && r <= 0x1f3ff: // emoji skin tone modifiers
		return true
	case r >= 0xe0020 && r <= 0xe007f: // tags (subdivision flags)
		return true
	case r >= 0x1160 && r <= 0x11ff, r >= 0xd7b0 && r <= 0xd7ff: // Hangul V and T jamo
		return true
	}
	return unicode.In(r, unicode.Mn, unicode.Me, unicode.Mc)
}

func isRegional(r rune) bool { return r >= 0x1f1e6 && r <= 0x1f1ff }

// filesOf returns the attachments of a post or scheduled message: from the
// metadata when the server sent it, else ids only. It is [] without files.
func filesOf(meta *model.PostMetadata, ids []string) []File {
	out := []File{}
	if meta != nil && len(meta.Files) > 0 {
		for _, f := range meta.Files {
			out = append(out, File{ID: f.Id, Name: f.Name, Size: f.Size, Mime: f.MimeType})
		}
		return out
	}
	for _, id := range ids {
		out = append(out, File{ID: id})
	}
	return out
}

// writeFiles writes one "[file]" line per attachment.
func writeFiles(b *strings.Builder, files []File) {
	for _, f := range files {
		if f.Name == "" {
			fmt.Fprintf(b, "  [file] %s\n", f.ID)
			continue
		}
		fmt.Fprintf(b, "  [file] %s (%s) %s\n", f.Name, HumanSize(f.Size), f.ID)
	}
}

// HumanSize formats a byte count with binary units.
func HumanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// JSON prints v indented, without HTML escaping.
func JSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
