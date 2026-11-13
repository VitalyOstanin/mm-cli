package cli

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"

	"github.com/VitalyOstanin/mm-cli/internal/fakemm"
)

// A failed teams lookup must not be remembered as "no team": the next call
// asks again and gets the real first team.
func TestLinkTeamRetriesAfterError(t *testing.T) {
	app, s := newTestApp(t)
	sess, err := app.openSession()
	if err != nil {
		t.Fatal(err)
	}
	sess.Team = ""
	var calls atomic.Int32
	s.Handle(http.MethodGet, "/api/v4/users/"+meID+"/teams", func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			fakemm.WriteJSON(w, http.StatusBadRequest, map[string]any{"id": "x", "message": "boom", "status_code": 400})
			return
		}
		fakemm.WriteJSON(w, http.StatusOK, []*model.Team{{Id: teamID, Name: "alpha"}})
	})
	ctx := context.Background()
	if got := sess.linkTeam(ctx); got != "" {
		t.Fatalf("first call = %q, want empty", got)
	}
	if got := sess.linkTeam(ctx); got != "alpha" {
		t.Fatalf("second call = %q, want alpha", got)
	}
	if got := sess.linkTeam(ctx); got != "alpha" || calls.Load() != 2 {
		t.Fatalf("third call = %q after %d requests, want cached alpha after 2", got, calls.Load())
	}
}
