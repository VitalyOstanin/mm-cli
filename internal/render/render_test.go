package render

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

var base = time.Date(2026, 3, 4, 10, 30, 0, 0, time.UTC).UnixMilli()

func TestFromModel(t *testing.T) {
	p := &model.Post{Id: "p1", ChannelId: "c1", UserId: "u1", Message: "hi", CreateAt: base, EditAt: base + 1, IsPinned: true, ReplyCount: 2,
		Metadata: &model.PostMetadata{Files: []*model.FileInfo{{Id: "f1", Name: "a.txt", Size: 12, MimeType: "text/plain"}}}}
	got := FromModel(p, map[string]string{"u1": "alice"})
	if got.CreateAt != "2026-03-04T10:30:00Z" || got.User.Username != "alice" || !got.Edited || !got.Pinned || got.ReplyCount != 2 {
		t.Fatalf("%+v", got)
	}
	if len(got.Files) != 1 || got.Files[0] != (File{ID: "f1", Name: "a.txt", Size: 12, Mime: "text/plain"}) {
		t.Fatalf("files %+v", got.Files)
	}
	bare := FromModel(&model.Post{Id: "p2", FileIds: model.StringArray{"f9"}}, nil)
	if len(bare.Files) != 1 || bare.Files[0].ID != "f9" || bare.User.Username != "" {
		t.Fatalf("bare %+v", bare)
	}
	var buf bytes.Buffer
	if err := JSON(&buf, FromModel(&model.Post{Id: "p3"}, nil)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `"files": []`) {
		t.Fatalf("files must be an empty array: %s", buf.String())
	}
}

func TestSorted(t *testing.T) {
	pl := &model.PostList{Posts: map[string]*model.Post{
		"b": {Id: "b", CreateAt: 2},
		"a": {Id: "a", CreateAt: 2},
		"c": {Id: "c", CreateAt: 1},
		"d": {Id: "d", CreateAt: 3, DeleteAt: 5},
	}}
	got := Sorted(pl)
	if len(got) != 3 || got[0].Id != "c" || got[1].Id != "a" || got[2].Id != "b" {
		t.Fatalf("%v", got)
	}
	if Sorted(nil) != nil {
		t.Fatal("nil list")
	}
}

func TestPostsText(t *testing.T) {
	posts := []Post{
		FromModel(&model.Post{Id: "p1", UserId: "u1", Message: "hello world", CreateAt: base, ReplyCount: 1,
			Metadata: &model.PostMetadata{Files: []*model.FileInfo{{Id: "f1", Name: "r.pdf", Size: 12_600}}}}, map[string]string{"u1": "alice"}),
		FromModel(&model.Post{Id: "p2", UserId: "u2", RootId: "p1", Message: "ok", CreateAt: base + 60_000, EditAt: 1}, nil),
	}
	var buf bytes.Buffer
	if err := PostsText(&buf, posts, time.UTC, 5); err != nil {
		t.Fatal(err)
	}
	want := "2026-03-04 10:30  @alice  id p1, replies 1\nhello…\n  [file] r.pdf (12.3 KiB) f1\n\n" +
		"2026-03-04 10:31  @u2  id p2, reply to p1, edited\nok\n"
	if buf.String() != want {
		t.Fatalf("got\n%q\nwant\n%q", buf.String(), want)
	}
}

func TestHumanSize(t *testing.T) {
	for n, want := range map[int64]string{0: "0 B", 1023: "1023 B", 1024: "1.0 KiB", 5 << 20: "5.0 MiB"} {
		if got := HumanSize(n); got != want {
			t.Errorf("HumanSize(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestJSONNoHTMLEscape(t *testing.T) {
	var buf bytes.Buffer
	_ = JSON(&buf, map[string]string{"m": "<a&b>"})
	if !strings.Contains(buf.String(), "<a&b>") {
		t.Fatal(buf.String())
	}
}

func TestShortenKeepsGraphemeClusters(t *testing.T) {
	for _, tc := range []struct {
		in   string
		n    int
		want string
	}{
		{"hello world", 5, "hello…"},
		{"short", 5, "short"},
		{"👨\u200d👩\u200d👧 ok", 1, "👨\u200d👩\u200d👧…"},
		{"👍🏽👍", 1, "👍🏽…"},
		{"🇷🇺🇺🇸🇩🇪", 2, "🇷🇺🇺🇸…"},
		{"е́ж", 1, "е́…"},
		{"е́ж", 2, "е́ж"},
		{"❤️!", 1, "❤️…"},
	} {
		if got := shorten(tc.in, tc.n); got != tc.want {
			t.Errorf("shorten(%q, %d) = %q, want %q", tc.in, tc.n, got, tc.want)
		}
	}
}

func TestSafeWriterStripsControls(t *testing.T) {
	var buf bytes.Buffer
	w := NewSafeWriter(&buf, nil)
	in := "a\x1b]0;evil\x07b\x1b[2Jc\r\nd\te\x9bf\u009bg\x7fh\n"
	if _, err := io.WriteString(w, in); err != nil {
		t.Fatal(err)
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	if strings.ContainsAny(got, "\x1b\x07\r\x7f\u009b") {
		t.Fatalf("control characters left: %q", got)
	}
	want := "a�]0;evil�b�[2Jc\nd\te�f�g�h\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestSafeWriterSplitUTF8(t *testing.T) {
	var buf bytes.Buffer
	w := NewSafeWriter(&buf, nil)
	s := []byte("пр👍")
	for i := range s {
		if n, err := w.Write(s[i : i+1]); err != nil || n != 1 {
			t.Fatalf("write %d: n=%d err=%v", i, n, err)
		}
	}
	_, _ = w.Write([]byte{0xd0}) // dangling first byte of a two-byte sequence
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); got != "пр👍�" {
		t.Fatalf("got %q", got)
	}
}

func TestSafeWriterDisabled(t *testing.T) {
	var buf bytes.Buffer
	w := NewSafeWriter(&buf, func() bool { return false })
	_, _ = io.WriteString(w, "a\x1b[2J\r\n")
	if got := buf.String(); got != "a\x1b[2J\r\n" {
		t.Fatalf("got %q", got)
	}
}

// The server also puts the root posts of threads into Posts; only the ids in
// Order are the answer.
func TestSortedFollowsOrder(t *testing.T) {
	pl := &model.PostList{
		Order: []string{"b", "a", "d", "a", "missing"},
		Posts: map[string]*model.Post{
			"a":    {Id: "a", CreateAt: 2},
			"b":    {Id: "b", CreateAt: 3},
			"d":    {Id: "d", CreateAt: 4, DeleteAt: 5},
			"root": {Id: "root", CreateAt: 1},
		},
	}
	got := Sorted(pl)
	if len(got) != 2 || got[0].Id != "a" || got[1].Id != "b" {
		ids := make([]string, len(got))
		for i, p := range got {
			ids[i] = p.Id
		}
		t.Fatalf("got %v, want [a b]", ids)
	}
}

// An order that names none of the posts is ignored rather than hiding them.
func TestSortedIgnoresUnrelatedOrder(t *testing.T) {
	pl := &model.PostList{
		Order: []string{"x"},
		Posts: map[string]*model.Post{"a": {Id: "a", CreateAt: 2}, "b": {Id: "b", CreateAt: 1}},
	}
	got := Sorted(pl)
	if len(got) != 2 || got[0].Id != "b" || got[1].Id != "a" {
		t.Fatalf("%v", got)
	}
	// Every listed post deleted: nothing to show, not the unlisted ones.
	pl = &model.PostList{
		Order: []string{"a"},
		Posts: map[string]*model.Post{"a": {Id: "a", DeleteAt: 1}, "root": {Id: "root"}},
	}
	if got := Sorted(pl); len(got) != 0 {
		t.Fatalf("%v", got)
	}
}
