package cli

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
)

func TestUsernamesErrReportsLookupFailure(t *testing.T) {
	app, s := newTestApp(t)
	s.JSON(http.MethodPost, "/api/v4/users/ids", 500, map[string]any{"message": "boom"})
	sess, err := app.openSession()
	if err != nil {
		t.Fatal(err)
	}
	got, err := sess.usernamesErr(context.Background(), []string{bobID})
	if err == nil {
		t.Fatal("want an error for a failed lookup")
	}
	if _, ok := got[bobID]; ok {
		t.Fatalf("unresolved id must be absent, got %v", got)
	}
	// usernames keeps its fallback: no error, id left out, one warning.
	if got := sess.usernames(context.Background(), []string{bobID}); len(got) != 0 {
		t.Fatalf("usernames = %v", got)
	}
	if n := strings.Count(app.Err.(interface{ String() string }).String(), "cannot look up user names"); n != 1 {
		t.Fatalf("warning count %d", n)
	}
}

func TestUsernamesErrNoErrorOnSuccess(t *testing.T) {
	app, s := newTestApp(t)
	s.JSON(http.MethodPost, "/api/v4/users/ids", 200, []*model.User{{Id: bobID, Username: "bob"}})
	sess, err := app.openSession()
	if err != nil {
		t.Fatal(err)
	}
	got, err := sess.usernamesErr(context.Background(), []string{bobID})
	if err != nil || got[bobID] != "bob" {
		t.Fatalf("got %v, %v", got, err)
	}
}
