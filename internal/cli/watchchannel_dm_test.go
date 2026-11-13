package cli

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"

	"github.com/VitalyOstanin/mm-cli/internal/fakemm"
)

// A passing failure to look up the other person of a direct channel must not
// pin the raw id as the channel name for the whole session.
func TestWatchChannelTransientPeerNameErrorIsRetried(t *testing.T) {
	w, srv := reactionWatcher(t)
	id := fakemm.ID("dm")
	me := w.s.Server.UserID
	srv.JSON(http.MethodGet, "/api/v4/channels/"+id, http.StatusOK,
		&model.Channel{Id: id, Name: model.GetDMNameFromIds(me, bobID), Type: model.ChannelTypeDirect})
	var healthy atomic.Bool
	srv.Handle(http.MethodPost, "/api/v4/users/ids", func(rw http.ResponseWriter, _ *http.Request) {
		if !healthy.Load() {
			fakemm.AppError(rw, http.StatusInternalServerError, "boom")
			return
		}
		fakemm.WriteJSON(rw, http.StatusOK, []*model.User{{Id: bobID, Username: "bob"}})
	})
	ctx := context.Background()
	if got := w.channel(ctx, id); got.name == "@bob" {
		t.Fatalf("first lookup should have failed, got name %q", got.name)
	}
	healthy.Store(true)
	if got := w.channel(ctx, id); got.name != "@bob" {
		t.Errorf("second lookup name = %q, want @bob", got.name)
	}
}
