package render

import (
	"strings"
	"testing"
	"time"
)

func TestThreadsText(t *testing.T) {
	threads := []Thread{
		{ID: "root1", Label: "#town-square", Root: Post{User: User{Username: "alice"}, Message: "first thread"},
			ReplyCount: 4, UnreadReplies: 1, LastReplyAt: "2026-03-04T12:30:00Z"},
		{ID: "root2", Label: "@bob", Root: Post{User: User{ID: "uid"}, Message: "a long message", CreateAt: "2026-03-04T10:00:00Z"}},
	}
	var b strings.Builder
	if err := ThreadsText(&b, threads, time.UTC, 6); err != nil {
		t.Fatal(err)
	}
	want := "2026-03-04 12:30  #town-square  @alice  id root1\n" +
		"replies 4, unread 1, mentions 0\n" +
		"first …\n" +
		"\n" +
		"2026-03-04 10:00  @bob  @uid  id root2\n" +
		"replies 0, unread 0, mentions 0\n" +
		"a long…\n"
	if b.String() != want {
		t.Fatalf("got:\n%s\nwant:\n%s", b.String(), want)
	}
}
