package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"

	"github.com/VitalyOstanin/mm-cli/internal/fakemm"
)

// reactionWatcher is a watcher over a fake server, for feeding it events.
func reactionWatcher(t *testing.T) (*watcher, *fakemm.Server) {
	t.Helper()
	app, srv := newTestApp(t)
	s, err := app.openSession()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return &watcher{
		s: s, reactions: true, posts: newPostCache(10),
		chans: map[string]watchChannel{}, teams: map[string]string{},
		seenPosts: newSeenSet(10), seenReactions: newSeenSet(10),
	}, srv
}

func postReactionEvent(t *testing.T, post, emoji string) *model.WebSocketEvent {
	t.Helper()
	b, err := json.Marshal(&model.Reaction{UserId: bobID, PostId: post, EmojiName: emoji, CreateAt: 1})
	if err != nil {
		t.Fatal(err)
	}
	ev := model.NewWebSocketEvent(model.WebsocketEventReactionAdded, "", fakemm.ID("chan"), "", nil, "")
	ev.Add("reaction", string(b))
	return ev
}

func TestWatchReactionPermanentFailureIsCached(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusForbidden} {
		w, srv := reactionWatcher(t)
		post := fakemm.ID("post")
		srv.Handle("GET", "/api/v4/posts/"+post, func(rw http.ResponseWriter, _ *http.Request) {
			fakemm.AppError(rw, status, "no")
		})
		for _, emoji := range []string{"+1", "heart"} {
			if err := w.reactionAdded(context.Background(), postReactionEvent(t, post, emoji)); err != nil {
				t.Fatal(err)
			}
		}
		if n := srv.Count("GET", "/api/v4/posts/"+post); n != 1 {
			t.Errorf("status %d: %d GET requests, want 1", status, n)
		}
	}
}

func TestWatchReactionTransientFailureIsRetried(t *testing.T) {
	w, srv := reactionWatcher(t)
	post := fakemm.ID("post")
	srv.Handle("GET", "/api/v4/posts/"+post, func(rw http.ResponseWriter, _ *http.Request) {
		fakemm.AppError(rw, http.StatusInternalServerError, "boom")
	})
	for _, emoji := range []string{"+1", "heart"} {
		if err := w.reactionAdded(context.Background(), postReactionEvent(t, post, emoji)); err != nil {
			t.Fatal(err)
		}
	}
	if n := srv.Count("GET", "/api/v4/posts/"+post); n < 2 {
		t.Errorf("%d GET requests after a 500, want a retry", n)
	}
}
