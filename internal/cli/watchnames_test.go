package cli

import (
	"context"
	"net/http"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"

	"github.com/VitalyOstanin/mm-cli/internal/fakemm"
)

func TestWatchPostNamesResolveAcknowledgers(t *testing.T) {
	w, srv := reactionWatcher(t)
	carol := fakemm.ID("carol")
	srv.JSON(http.MethodPost, "/api/v4/users/ids", http.StatusOK, []*model.User{{Id: carol, Username: "carol"}})
	p := &model.Post{Id: fakemm.ID("p"), UserId: bobID, Metadata: &model.PostMetadata{
		Acknowledgements: []*model.PostAcknowledgement{{UserId: carol}},
	}}
	got := w.postNames(context.Background(), p, "@bob")
	if got[bobID] != "bob" || got[carol] != "carol" {
		t.Errorf("names = %v", got)
	}
}

func TestWatchPostNamesLookupFailureKeepsPost(t *testing.T) {
	w, srv := reactionWatcher(t)
	srv.JSON(http.MethodPost, "/api/v4/users/ids", http.StatusInternalServerError, map[string]any{"message": "boom"})
	p := &model.Post{Id: fakemm.ID("p"), UserId: bobID, Metadata: &model.PostMetadata{
		Acknowledgements: []*model.PostAcknowledgement{{UserId: fakemm.ID("carol")}},
	}}
	got := w.postNames(context.Background(), p, "@bob")
	if got[bobID] != "bob" || len(got) != 1 {
		t.Errorf("names = %v", got)
	}
}

func TestWatchPostNamesPlainPostAsksNothing(t *testing.T) {
	w, srv := reactionWatcher(t)
	p := &model.Post{Id: fakemm.ID("p"), UserId: bobID}
	if got := w.postNames(context.Background(), p, "@bob"); got[bobID] != "bob" {
		t.Errorf("names = %v", got)
	}
	if n := srv.Count(http.MethodPost, "/api/v4/users/ids"); n != 0 {
		t.Errorf("%d lookups", n)
	}
}
