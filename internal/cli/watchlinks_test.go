package cli

import (
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"

	"github.com/VitalyOstanin/mm-cli/internal/fakemm"
)

// noSessionTeam drops the saved server's default team, as for a server added
// without --team.
func noSessionTeam(t *testing.T, app *App) {
	t.Helper()
	cfg, err := app.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Servers["test"].Team = ""
	if err := app.Store.Save(cfg); err != nil {
		t.Fatal(err)
	}
}

// serveDirectTeams serves two teams listed out of name order, and two direct
// channels with bob and carol.
func serveDirectTeams(s *fakemm.Server, dm1, dm2 string) {
	s.JSON(http.MethodGet, "/api/v4/users/"+meID+"/teams", 200, []*model.Team{{Id: team2ID, Name: "zeta"}, {Id: teamID, Name: "alpha"}})
	s.JSON(http.MethodGet, "/api/v4/channels/"+dm1, 200, &model.Channel{Id: dm1, Name: meID + "__" + bobID, Type: model.ChannelTypeDirect})
	s.JSON(http.MethodGet, "/api/v4/channels/"+dm2, 200, &model.Channel{Id: dm2, Name: meID + "__" + reactCarol, Type: model.ChannelTypeDirect})
	s.JSON(http.MethodPost, "/api/v4/users/ids", 200, []*model.User{{Id: bobID, Username: "bob"}, {Id: reactCarol, Username: "carol"}, {Id: meID, Username: "alice"}})
}

func TestWatchDirectLinksUseFirstTeamAskedOnce(t *testing.T) {
	app, s := newTestApp(t)
	noSessionTeam(t, app)
	dm1, dm2 := fakemm.ID("dm1"), fakemm.ID("dm2")
	serveDirectTeams(s, dm1, dm2)
	p1, p2, p3 := fakemm.ID("p1"), fakemm.ID("p2"), fakemm.ID("p3")
	for id, ch := range map[string]string{p1: dm1, p2: dm2} {
		s.JSON(http.MethodGet, "/api/v4/posts/"+id, 200, &model.Post{Id: id, UserId: meID, ChannelId: ch, Message: "m " + id})
	}
	var mu sync.Mutex
	conns := 0
	s.Handle(http.MethodGet, "/api/v4/websocket", wsHandler(t, "tok", [][]byte{
		reactionEvent(t, dm1, p1, bobID, "eyes"),
		reactionEvent(t, dm2, p2, reactCarol, "heart"),
		postedBy(t, dm1, bobID, "@bob", p3, "hello"),
	}, true, &conns, &mu))
	got := notifyRecorder(app)
	out, _, stop := startWatch(t, app, "--messages", "--reactions", "--notify")
	waitFor(t, "all events", func() bool { return strings.Contains(out.String(), "hello") && len(got()) >= 3 })
	stop()
	for _, want := range []string{"/alpha/pl/" + p1, "/alpha/pl/" + p2} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "//pl/") {
		t.Errorf("empty team segment:\n%s", out.String())
	}
	for _, n := range got() {
		if !strings.Contains(n.AppLink, "/alpha/pl/") {
			t.Errorf("notification link %q", n.AppLink)
		}
	}
	if n := s.Count(http.MethodGet, "/api/v4/users/"+meID+"/teams"); n != 1 {
		t.Errorf("%d GetTeamsForUser calls, want 1", n)
	}
}

func TestWatchCatchUpDirectLinkUsesFirstTeam(t *testing.T) {
	app, s := newTestApp(t)
	noSessionTeam(t, app)
	noSleep(app)
	dm := fakemm.ID("dm1")
	p1 := fakemm.ID("p1")
	pl := &model.PostList{Order: []string{p1}, Posts: map[string]*model.Post{p1: reactionPost(p1, dm, 2*time.Hour, reaction(p1, bobID, "eyes", time.Second))}}
	s.JSON(http.MethodPost, "/api/v4/posts/search", 200, pl)
	s.JSON(http.MethodGet, "/api/v4/users/"+meID+"/teams", 200, []*model.Team{{Id: team2ID, Name: "zeta"}, {Id: teamID, Name: "alpha"}})
	ch := &model.Channel{Id: dm, Name: meID + "__" + bobID, Type: model.ChannelTypeDirect, LastPostAt: now.UnixMilli()}
	s.JSON(http.MethodGet, "/api/v4/users/"+meID+"/channels", 200, []*model.Channel{ch})
	s.JSON(http.MethodGet, "/api/v4/channels/"+dm, 200, ch)
	s.JSON(http.MethodPost, "/api/v4/users/ids", 200, []*model.User{{Id: bobID, Username: "bob"}, {Id: meID, Username: "alice"}})
	s.JSON(http.MethodGet, "/api/v4/channels/"+dm+"/posts", 200, &model.PostList{Order: []string{fakemm.ID("m1")}, Posts: map[string]*model.Post{fakemm.ID("m1"): missed(fakemm.ID("m1"), dm, "missed text")}})
	s.Handle(http.MethodGet, "/api/v4/websocket", wsPerConn(t, "tok", [][][]byte{nil, nil}))
	got := notifyRecorder(app)
	out, _, stop := startWatch(t, app, "--messages", "--reactions", "--notify")
	waitFor(t, "the catch-up", func() bool {
		return strings.Contains(out.String(), ":eyes:") && strings.Contains(out.String(), "missed text") && len(got()) >= 2
	})
	stop()
	if !strings.Contains(out.String(), "/alpha/pl/"+p1) || strings.Contains(out.String(), "//pl/") {
		t.Errorf("reaction link:\n%s", out.String())
	}
	for _, n := range got() {
		if !strings.Contains(n.AppLink, "/alpha/pl/") {
			t.Errorf("notification link %q", n.AppLink)
		}
	}
	if n := s.Count(http.MethodGet, "/api/v4/users/"+meID+"/teams"); n != 1 {
		t.Errorf("%d GetTeamsForUser calls, want 1", n)
	}
}

func TestPostReactionListDirectLinkFirstTeam(t *testing.T) {
	app, s := newTestApp(t)
	noSessionTeam(t, app)
	p := fakemm.ID("p1")
	dm := fakemm.ID("dm1")
	s.JSON(http.MethodGet, "/api/v4/posts/"+p, 200, &model.Post{Id: p, UserId: meID, ChannelId: dm, Message: "hello"})
	s.JSON(http.MethodGet, "/api/v4/posts/"+p+"/reactions", 200, []*model.Reaction{reaction(p, bobID, "heart", time.Minute)})
	s.JSON(http.MethodGet, "/api/v4/channels/"+dm, 200, &model.Channel{Id: dm, Name: meID + "__" + bobID, Type: model.ChannelTypeDirect})
	s.JSON(http.MethodGet, "/api/v4/users/"+meID+"/teams", 200, []*model.Team{{Id: team2ID, Name: "zeta"}, {Id: teamID, Name: "alpha"}})
	s.JSON(http.MethodPost, "/api/v4/users/ids", 200, []*model.User{{Id: bobID, Username: "bob"}})
	r := run(app, "post", "reaction", "list", p, "--json")
	mustCode(t, r, 0)
	if !strings.Contains(r.out, s.URL+"/alpha/pl/"+p) {
		t.Errorf("permalink: %s", r.out)
	}
}
