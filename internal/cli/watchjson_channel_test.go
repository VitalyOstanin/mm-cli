package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"

	"github.com/VitalyOstanin/mm-cli/internal/fakemm"
)

// channelLine is the channel part of a `watch --json` line.
type channelLine struct {
	Type               string `json:"type"`
	Channel            string `json:"channel"`
	ChannelDisplayName string `json:"channel_display_name"`
	Team               string `json:"team"`
}

func parseChannelLines(t *testing.T, out string) []channelLine {
	t.Helper()
	var ls []channelLine
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		var l channelLine
		if err := json.Unmarshal([]byte(line), &l); err != nil {
			t.Fatalf("bad line %q: %v", line, err)
		}
		ls = append(ls, l)
	}
	return ls
}

// jsonPostWatcher is a watcher over a fake server that prints JSON.
func jsonPostWatcher(t *testing.T) (*watcher, *fakemm.Server, *bytes.Buffer) {
	t.Helper()
	w, srv := reactionWatcher(t)
	w.messages = true
	w.s.app.json = true
	var buf bytes.Buffer
	w.s.app.Out = &buf
	srv.JSON(http.MethodGet, "/api/v4/teams/"+teamID, 200, &model.Team{Id: teamID, Name: "myteam"})
	srv.JSON(http.MethodGet, "/api/v4/users/"+meID+"/teams", 200, []*model.Team{{Id: teamID, Name: "myteam"}})
	srv.JSON(http.MethodPost, "/api/v4/users/ids", 200, []*model.User{{Id: bobID, Username: "bob"}, {Id: meID, Username: "alice"}})
	return w, srv, &buf
}

func livePost(t *testing.T, w *watcher, channelID, label, id string) {
	t.Helper()
	ev, err := model.WebSocketEventFromJSON(bytes.NewReader(postedEvent(t, channelID, label, "@bob", id, "hi")))
	if err != nil {
		t.Fatal(err)
	}
	if err := w.posted(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
}

func TestWatchJSONLivePostInOpenChannel(t *testing.T) {
	w, srv, buf := jsonPostWatcher(t)
	srv.JSON(http.MethodGet, "/api/v4/channels/"+townSq, 200,
		&model.Channel{Id: townSq, TeamId: teamID, Name: "town-square", DisplayName: "Town Square", Type: model.ChannelTypeOpen})
	livePost(t, w, townSq, "Town Square", fakemm.ID("p1"))
	livePost(t, w, townSq, "Town Square", fakemm.ID("p2"))
	ls := parseChannelLines(t, buf.String())
	if len(ls) != 2 {
		t.Fatalf("lines: %s", buf.String())
	}
	for _, l := range ls {
		if l != (channelLine{"post", "town-square", "Town Square", "myteam"}) {
			t.Errorf("line = %+v", l)
		}
	}
	// The channel and its team are asked for once, whatever the posts.
	if n := srv.Count(http.MethodGet, "/api/v4/channels/"+townSq); n != 1 {
		t.Errorf("%d channel requests, want 1", n)
	}
	if n := srv.Count(http.MethodGet, "/api/v4/teams/"+teamID); n != 1 {
		t.Errorf("%d team requests, want 1", n)
	}
}

func TestWatchJSONLivePostInDirectChannel(t *testing.T) {
	w, srv, buf := jsonPostWatcher(t)
	dm := fakemm.ID("dm1")
	srv.JSON(http.MethodGet, "/api/v4/channels/"+dm, 200, &model.Channel{Id: dm, Name: meID + "__" + bobID, Type: model.ChannelTypeDirect})
	livePost(t, w, dm, "@bob", fakemm.ID("p1"))
	ls := parseChannelLines(t, buf.String())
	if len(ls) != 1 || ls[0] != (channelLine{"post", "@bob", "@bob", ""}) {
		t.Errorf("lines = %+v", ls)
	}
}

func TestWatchJSONTextOutputKeepsDisplayLabel(t *testing.T) {
	w, srv, buf := jsonPostWatcher(t)
	w.s.app.json = false
	srv.JSON(http.MethodGet, "/api/v4/channels/"+townSq, 200,
		&model.Channel{Id: townSq, TeamId: teamID, Name: "town-square", DisplayName: "Town Square", Type: model.ChannelTypeOpen})
	livePost(t, w, townSq, "Town Square", fakemm.ID("p1"))
	if !strings.HasPrefix(buf.String(), "[Town Square] ") {
		t.Errorf("text = %q", buf.String())
	}
	if n := srv.Count(http.MethodGet, "/api/v4/channels/"+townSq); n != 0 {
		t.Errorf("text mode asked for the channel %d times", n)
	}
}

func TestWatchJSONReactionInGroupChannel(t *testing.T) {
	w, srv, buf := jsonPostWatcher(t)
	w.reactions = true
	grp := fakemm.ID("grp")
	post := fakemm.ID("post")
	srv.JSON(http.MethodGet, "/api/v4/channels/"+grp, 200,
		&model.Channel{Id: grp, Name: "hash", DisplayName: "bob, carol", Type: model.ChannelTypeGroup})
	w.posts.put(postInfo{id: post, author: meID, channelID: grp})
	if err := w.reactionAdded(context.Background(), postReactionEvent(t, post, "tada")); err != nil {
		t.Fatal(err)
	}
	ls := parseChannelLines(t, buf.String())
	if len(ls) != 1 || ls[0] != (channelLine{"reaction", grp, "bob, carol", ""}) {
		t.Errorf("lines = %+v", ls)
	}

	// The text keeps the display name.
	buf.Reset()
	w.s.app.json = false
	if err := w.reactionAdded(context.Background(), postReactionEvent(t, post, "heart")); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(buf.String(), "[bob, carol] ") {
		t.Errorf("text = %q", buf.String())
	}
}

func TestWatchJSONCatchUpPostHasSameFieldsAsLive(t *testing.T) {
	app, s := newTestApp(t)
	noSleep(app)
	ch := newestChannel(townSq, "town-square", now)
	ch.DisplayName = "Town Square"
	catchUpServer(s, []*model.Channel{ch},
		map[string][]*model.Post{townSq: {missed(fakemm.ID("p1"), townSq, "written during the drop")}})
	s.Handle(http.MethodGet, "/api/v4/websocket", wsPerConn(t, "tok", [][][]byte{nil, {helloFrame}}))
	out, _, stop := startWatch(t, app, "--json")
	waitFor(t, "the catch-up", func() bool { return strings.Contains(out.String(), "written during the drop") })
	stop()
	ls := parseChannelLines(t, out.String())
	if len(ls) != 1 || ls[0] != (channelLine{"post", "town-square", "Town Square", "myteam"}) {
		t.Errorf("lines = %+v\n%s", ls, out.String())
	}
}
