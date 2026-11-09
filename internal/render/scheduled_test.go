package render

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

func ms(year int, month time.Month, day, hour, min int) int64 {
	return time.Date(year, month, day, hour, min, 0, 0, time.UTC).UnixMilli()
}

func TestScheduledFrom(t *testing.T) {
	cases := []struct {
		name string
		in   *model.ScheduledPost
		want Scheduled
	}{
		{
			name: "plain",
			in: &model.ScheduledPost{
				Draft:       model.Draft{ChannelId: "chan1", Message: "hello", CreateAt: ms(2026, 3, 1, 9, 0), UpdateAt: ms(2026, 3, 2, 10, 15)},
				Id:          "sp1",
				ScheduledAt: ms(2026, 3, 4, 12, 30),
			},
			want: Scheduled{
				ID: "sp1", ChannelID: "chan1", Channel: "chan1", Label: "chan1", Message: "hello",
				Files:       []File{},
				ScheduledAt: "2026-03-04T12:30:00Z", CreateAt: "2026-03-01T09:00:00Z", UpdateAt: "2026-03-02T10:15:00Z",
			},
		},
		{
			name: "failed reply with weekly repeat",
			in: &model.ScheduledPost{
				Draft:       model.Draft{ChannelId: "chan1", RootId: "root1", Message: "again"},
				Id:          "sp2",
				ScheduledAt: ms(2026, 3, 4, 12, 30),
				RepeatType:  "weekly",
				ErrorCode:   "channel_archived",
			},
			want: Scheduled{
				ID: "sp2", ChannelID: "chan1", Channel: "chan1", Label: "chan1", RootID: "root1", Message: "again",
				Files:       []File{},
				ScheduledAt: "2026-03-04T12:30:00Z", Repeat: "weekly", Error: "channel_archived",
			},
		},
		{
			name: "files from metadata win over ids",
			in: &model.ScheduledPost{
				Draft: model.Draft{
					ChannelId: "chan1",
					FileIds:   []string{"ignored"},
					Metadata: &model.PostMetadata{Files: []*model.FileInfo{
						{Id: "f1", Name: "a.txt", Size: 2048, MimeType: "text/plain"},
					}},
				},
				Id: "sp3",
			},
			want: Scheduled{
				ID: "sp3", ChannelID: "chan1", Channel: "chan1", Label: "chan1",
				Files: []File{{ID: "f1", Name: "a.txt", Size: 2048, Mime: "text/plain"}},
			},
		},
		{
			name: "bare file ids",
			in:   &model.ScheduledPost{Draft: model.Draft{ChannelId: "chan1", FileIds: []string{"f1", "f2"}}, Id: "sp4"},
			want: Scheduled{
				ID: "sp4", ChannelID: "chan1", Channel: "chan1", Label: "chan1",
				Files: []File{{ID: "f1"}, {ID: "f2"}},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ScheduledFrom(c.in)
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got:  %+v\nwant: %+v", got, c.want)
			}
		})
	}
}

func TestScheduledText(t *testing.T) {
	zone := time.FixedZone("UTC+3", 3*3600)
	list := []Scheduled{
		{ID: "sp1", Label: "#town-square", Message: "hello", ScheduledAt: "2026-03-04T12:30:00Z"},
		{ID: "sp2", Label: "@bob", RootID: "root1", Repeat: "weekly", Error: "channel_archived",
			Message: "again", ScheduledAt: "2026-03-04T22:30:00Z"},
		{ID: "sp3", Label: "group", ScheduledAt: "2026-03-05T00:00:00Z", Files: []File{
			{ID: "f1", Name: "a.txt", Size: 2048}, {ID: "f2"},
		}},
	}
	var b strings.Builder
	if err := ScheduledText(&b, list, zone); err != nil {
		t.Fatal(err)
	}
	want := "2026-03-04 15:30  #town-square  id sp1\n" +
		"hello\n" +
		"\n" +
		"2026-03-05 01:30  @bob  id sp2, reply to root1, weekly, failed: channel_archived\n" +
		"again\n" +
		"\n" +
		"2026-03-05 03:00  group  id sp3\n" +
		"  [file] a.txt (2.0 KiB) f1\n" +
		"  [file] f2\n"
	if b.String() != want {
		t.Fatalf("got:\n%s\nwant:\n%s", b.String(), want)
	}
}

func TestScheduledTextEmpty(t *testing.T) {
	var b strings.Builder
	if err := ScheduledText(&b, nil, time.UTC); err != nil {
		t.Fatal(err)
	}
	if b.Len() != 0 {
		t.Fatalf("got %q, want empty output", b.String())
	}
}
