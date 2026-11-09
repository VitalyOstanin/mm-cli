package render

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

// Scheduled is one scheduled message. The JSON field names are part of the
// public interface.
type Scheduled struct {
	ID        string `json:"id"`
	ChannelID string `json:"channel_id"`
	// Channel is an address mm-cli accepts: team:name, @user, or an id.
	Channel string `json:"channel"`
	RootID  string `json:"root_id"`
	Message string `json:"message"`
	Files   []File `json:"files"`
	// ScheduledAt, CreateAt and UpdateAt are RFC 3339 in UTC.
	ScheduledAt string `json:"scheduled_at"`
	// Repeat is "" or "weekly".
	Repeat string `json:"repeat"`
	// Error is the server's reason a message could not be sent, or "".
	Error    string `json:"error"`
	CreateAt string `json:"create_at"`
	UpdateAt string `json:"update_at"`
	// Label is the channel as text output shows it: #name, @user or group.
	Label string `json:"-"`
}

func rfc3339(ms int64) string {
	if ms == 0 {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format(time.RFC3339)
}

// ScheduledFrom converts a scheduled message; the caller fills in Channel and
// Label.
func ScheduledFrom(sp *model.ScheduledPost) Scheduled {
	out := Scheduled{
		ID:          sp.Id,
		ChannelID:   sp.ChannelId,
		Channel:     sp.ChannelId,
		RootID:      sp.RootId,
		Message:     sp.Message,
		Files:       []File{},
		ScheduledAt: rfc3339(sp.ScheduledAt),
		Repeat:      sp.RepeatType,
		Error:       sp.ErrorCode,
		CreateAt:    rfc3339(sp.CreateAt),
		UpdateAt:    rfc3339(sp.UpdateAt),
		Label:       sp.ChannelId,
	}
	if sp.Metadata != nil && len(sp.Metadata.Files) > 0 {
		for _, f := range sp.Metadata.Files {
			out.Files = append(out.Files, File{ID: f.Id, Name: f.Name, Size: f.Size, Mime: f.MimeType})
		}
	} else {
		for _, id := range sp.FileIds {
			out.Files = append(out.Files, File{ID: id})
		}
	}
	return out
}

// ScheduledText prints scheduled messages for people, in the given time zone.
func ScheduledText(w io.Writer, list []Scheduled, loc *time.Location) error {
	var b strings.Builder
	for i, s := range list {
		if i > 0 {
			b.WriteString("\n")
		}
		ts := s.ScheduledAt
		if t, err := time.Parse(time.RFC3339, s.ScheduledAt); err == nil {
			ts = t.In(loc).Format("2006-01-02 15:04")
		}
		fmt.Fprintf(&b, "%s  %s  id %s", ts, s.Label, s.ID)
		if s.RootID != "" {
			fmt.Fprintf(&b, ", reply to %s", s.RootID)
		}
		if s.Repeat != "" {
			fmt.Fprintf(&b, ", %s", s.Repeat)
		}
		if s.Error != "" {
			fmt.Fprintf(&b, ", failed: %s", s.Error)
		}
		b.WriteString("\n")
		if s.Message != "" {
			b.WriteString(s.Message)
			b.WriteString("\n")
		}
		for _, f := range s.Files {
			if f.Name == "" {
				fmt.Fprintf(&b, "  [file] %s\n", f.ID)
				continue
			}
			fmt.Fprintf(&b, "  [file] %s (%s) %s\n", f.Name, HumanSize(f.Size), f.ID)
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}
