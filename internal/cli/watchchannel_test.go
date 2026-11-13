package cli

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"

	"github.com/VitalyOstanin/mm-cli/internal/fakemm"
)

// A passing team lookup failure must not pin the fallback for the whole
// session: the next event asks again and gets the real team.
func TestWatchChannelTransientTeamErrorIsRetried(t *testing.T) {
	w, srv := reactionWatcher(t)
	id, team := fakemm.ID("chan"), fakemm.ID("team")
	srv.JSON(http.MethodGet, "/api/v4/channels/"+id, http.StatusOK,
		&model.Channel{Id: id, Name: "town-square", DisplayName: "Town Square", Type: model.ChannelTypeOpen, TeamId: team})
	// The session's own team (myteam) is the fallback, so the real one differs.
	// The client retries a 5xx itself, so the outage is switched off by the
	// test rather than by a call count.
	var healthy atomic.Bool
	srv.Handle(http.MethodGet, "/api/v4/teams/"+team, func(rw http.ResponseWriter, _ *http.Request) {
		if !healthy.Load() {
			fakemm.AppError(rw, http.StatusInternalServerError, "boom")
			return
		}
		fakemm.WriteJSON(rw, http.StatusOK, &model.Team{Id: team, Name: "otherteam"})
	})
	ctx := context.Background()
	if got := w.channel(ctx, id); got.team == "otherteam" {
		t.Fatalf("first lookup should have failed, got team %q", got.team)
	}
	healthy.Store(true)
	if got := w.channel(ctx, id); got.team != "otherteam" || got.name == id {
		t.Errorf("second lookup = %+v, want team otherteam and a real name", got)
	}
}
