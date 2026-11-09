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

// ScheduledFrom converts a scheduled message; the caller fills in Channel and
// Label.
func ScheduledFrom(sp *model.ScheduledPost) Scheduled {
	out := Scheduled{
		ID:          sp.Id,
		ChannelID:   sp.ChannelId,
		Channel:     sp.ChannelId,
		RootID:      sp.RootId,
		Message:     sp.Message,
		ScheduledAt: RFC3339(sp.ScheduledAt),
		Repeat:      sp.RepeatType,
		Error:       sp.ErrorCode,
		CreateAt:    RFC3339(sp.CreateAt),
		UpdateAt:    RFC3339(sp.UpdateAt),
		Label:       sp.ChannelId,
	}
	out.Files = filesOf(sp.Metadata, sp.FileIds)
	return out
}

// ScheduledText prints scheduled messages for people, in the given time zone.
func ScheduledText(w io.Writer, list []Scheduled, loc *time.Location) error {
	var b strings.Builder
	for i, s := range list {
		if i > 0 {
			b.WriteString("\n")
		}
		ts := LocalTime(s.ScheduledAt, loc)
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
		writeFiles(&b, s.Files)
	}
	_, err := io.WriteString(w, b.String())
	return err
}
