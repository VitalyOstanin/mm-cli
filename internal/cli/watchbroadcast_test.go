package cli

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"

	"github.com/VitalyOstanin/mm-cli/internal/fakemm"
)

// A frame that carries no "broadcast" is decoded to an event whose
// GetBroadcast is nil; the handlers must skip or tolerate it, not panic.
func TestWatchEventsWithoutBroadcast(t *testing.T) {
	w, srv := reactionWatcher(t)
	post := fakemm.ID("post")
	srv.Handle("GET", "/api/v4/posts/"+post, func(rw http.ResponseWriter, _ *http.Request) {
		fakemm.AppError(rw, http.StatusNotFound, "no")
	})
	reaction := `{"user_id":"` + bobID + `","post_id":"` + post + `","emoji_name":"+1","create_at":1}`
	frames := []string{
		`{"event":"reaction_added","data":{"reaction":` + quoteJSON(reaction) + `}}`,
		`{"event":"posted","data":{"post":` + quoteJSON(`{"id":"`+post+`","channel_id":"`+fakemm.ID("chan")+`","user_id":"`+bobID+`","message":"hi"}`) + `}}`,
	}
	for _, f := range frames {
		ev, err := model.WebSocketEventFromJSON(strings.NewReader(f))
		if err != nil {
			t.Fatal(err)
		}
		if ev.GetBroadcast() != nil {
			t.Fatalf("expected a nil broadcast for %s", f)
		}
		switch ev.EventType() {
		case model.WebsocketEventReactionAdded:
			err = w.reactionAdded(context.Background(), ev)
		default:
			err = w.posted(context.Background(), ev)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
}

func quoteJSON(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}
