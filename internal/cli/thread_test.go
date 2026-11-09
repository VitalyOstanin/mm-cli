package cli

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"

	"github.com/VitalyOstanin/mm-cli/internal/fakemm"
	"github.com/VitalyOstanin/mm-cli/internal/render"
)

var (
	rootOpen = fakemm.ID("rootopen")
	rootDM   = fakemm.ID("rootdm")
	dmChan   = fakemm.ID("dmchan")
	team2ID  = fakemm.ID("team2")
)

func threadsPath(team string) string {
	return "/api/v4/users/" + meID + "/teams/" + team + "/threads"
}

// serveThreads serves one open-channel thread and one direct-channel thread
// for myteam, with the channel list and user names they need.
func serveThreads(s *fakemm.Server) {
	serveTownSquare(s)
	s.JSON(http.MethodGet, "/api/v4/users/"+meID+"/teams/"+teamID+"/channels", 200, []*model.Channel{
		{Id: townSq, Name: "town-square", Type: model.ChannelTypeOpen, TeamId: teamID},
		{Id: dmChan, Name: meID + "__" + bobID, Type: model.ChannelTypeDirect},
	})
	s.JSON(http.MethodGet, threadsPath(teamID), 200, &model.Threads{Threads: []*model.ThreadResponse{
		{PostId: rootDM, ReplyCount: 1, LastReplyAt: 1000, Post: &model.Post{Id: rootDM, ChannelId: dmChan, UserId: bobID, Message: "dm root", CreateAt: 500}},
		{PostId: rootOpen, ReplyCount: 4, UnreadReplies: 2, UnreadMentions: 1, LastReplyAt: 2000,
			Post: &model.Post{Id: rootOpen, ChannelId: townSq, UserId: meID, Message: "open root", CreateAt: 900}},
	}})
}

func TestThreadList(t *testing.T) {
	app, s := newTestApp(t)
	serveThreads(s)

	r := run(app, "thread", "list")
	mustCode(t, r, 0)
	want := "1970-01-01 00:00  #town-square  @alice  id " + rootOpen + "\n" +
		"replies 4, unread 2, mentions 1\nopen root\n\n" +
		"1970-01-01 00:00  @bob  @bob  id " + rootDM + "\n" +
		"replies 1, unread 0, mentions 0\ndm root\n"
	if r.out != want {
		t.Fatalf("got:\n%s\nwant:\n%s", r.out, want)
	}
	q := s.Last(http.MethodGet, threadsPath(teamID)).Query
	if q.Get("extended") != "true" || q.Get("per_page") != "20" || q.Get("unread") != "" {
		t.Fatalf("query: %v", q)
	}

	mustCode(t, run(app, "thread", "list", "--unread", "-n", "5"), 0)
	q = s.Last(http.MethodGet, threadsPath(teamID)).Query
	if q.Get("unread") != "true" || q.Get("per_page") != "5" {
		t.Fatalf("query with --unread: %v", q)
	}
}

func TestThreadListJSON(t *testing.T) {
	app, s := newTestApp(t)
	serveThreads(s)
	var got []render.Thread
	mustJSON(t, run(app, "thread", "list", "--json"), &got)
	if len(got) != 2 {
		t.Fatalf("got %d threads", len(got))
	}
	open := got[0]
	if open.ID != rootOpen || open.Team != "myteam" || open.ChannelID != townSq || open.Channel != "myteam/town-square" ||
		open.ChannelType != "O" || open.Root.Message != "open root" || open.ReplyCount != 4 || open.Root.ReplyCount != 4 ||
		open.UnreadReplies != 2 || open.UnreadMentions != 1 || open.LastReplyAt != "1970-01-01T00:00:02Z" {
		t.Fatalf("open thread: %+v", open)
	}
	if got[1].Channel != "@bob" || got[1].ChannelType != "D" {
		t.Fatalf("dm thread: %+v", got[1])
	}
	// The field names are a public interface.
	r := run(app, "thread", "list", "--json")
	var raw []map[string]any
	if err := json.Unmarshal([]byte(r.out), &raw); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"id", "team", "channel_id", "channel", "channel_type", "root", "reply_count",
		"unread_replies", "unread_mentions", "last_reply_at"} {
		if _, ok := raw[0][k]; !ok {
			t.Errorf("JSON lacks %q", k)
		}
	}
}

// TestThreadListAllTeams: without a team the threads of every team are
// merged, newest reply first, and a direct-channel thread that the server
// returns for both teams is shown once.
func TestThreadListAllTeams(t *testing.T) {
	app, s := newTestApp(t)
	cfg, err := app.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Profiles["test"].Team = ""
	if err := app.Store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	other := fakemm.ID("rootother")
	s.JSON(http.MethodGet, "/api/v4/users/"+meID+"/teams", 200, []*model.Team{{Id: teamID, Name: "myteam"}, {Id: team2ID, Name: "team2"}})
	s.JSON(http.MethodGet, "/api/v4/users/"+meID+"/channels", 200, []*model.Channel{
		{Id: townSq, Name: "town-square", Type: model.ChannelTypeOpen, TeamId: teamID},
		{Id: dmChan, Name: meID + "__" + bobID, Type: model.ChannelTypeDirect},
	})
	s.JSON(http.MethodPost, "/api/v4/users/ids", 200, []*model.User{{Id: meID, Username: "alice"}, {Id: bobID, Username: "bob"}})
	dm := &model.ThreadResponse{PostId: rootDM, LastReplyAt: 1000, Post: &model.Post{Id: rootDM, ChannelId: dmChan, UserId: bobID}}
	s.JSON(http.MethodGet, threadsPath(teamID), 200, &model.Threads{Threads: []*model.ThreadResponse{dm}})
	s.JSON(http.MethodGet, threadsPath(team2ID), 200, &model.Threads{Threads: []*model.ThreadResponse{
		{PostId: other, LastReplyAt: 3000, Post: &model.Post{Id: other, ChannelId: townSq, UserId: meID}}, dm,
	}})

	var got []render.Thread
	mustJSON(t, run(app, "thread", "list", "--json"), &got)
	if len(got) != 2 || got[0].ID != other || got[1].ID != rootDM {
		t.Fatalf("threads: %+v", got)
	}
	if !strings.Contains(run(app, "thread", "list").out, "#myteam/town-square") {
		t.Fatal("text output lacks the team-qualified channel")
	}
}

// TestThreadListDoesNotMarkRead: reading sends no request that marks anything read.
func TestThreadListDoesNotMarkRead(t *testing.T) {
	app, s := newTestApp(t)
	serveThreads(s)
	mustCode(t, run(app, "thread", "list", "--unread"), 0)
	for _, p := range []string{threadsPath(teamID) + "/read", threadsPath(teamID) + "/" + rootOpen + "/read"} {
		if n := s.Count(http.MethodPut, p); n != 0 {
			t.Fatalf("%s called %d times", p, n)
		}
	}
}

// serveThreadPosts serves a root post in town-square, a reply to it and a
// root post in a direct channel.
func serveThreadPosts(s *fakemm.Server) (reply string) {
	reply = fakemm.ID("reply")
	s.JSON(http.MethodGet, "/api/v4/posts/"+rootOpen, 200, &model.Post{Id: rootOpen, ChannelId: townSq})
	s.JSON(http.MethodGet, "/api/v4/posts/"+reply, 200, &model.Post{Id: reply, ChannelId: townSq, RootId: rootOpen})
	s.JSON(http.MethodGet, "/api/v4/posts/"+rootDM, 200, &model.Post{Id: rootDM, ChannelId: dmChan})
	s.JSON(http.MethodGet, "/api/v4/channels/"+townSq, 200, &model.Channel{Id: townSq, TeamId: teamID, Type: model.ChannelTypeOpen})
	s.JSON(http.MethodGet, "/api/v4/channels/"+dmChan, 200, &model.Channel{Id: dmChan, Type: model.ChannelTypeDirect})
	s.JSON(http.MethodGet, "/api/v4/teams/name/myteam", 200, &model.Team{Id: teamID, Name: "myteam"})
	return reply
}

func TestThreadFollow(t *testing.T) {
	app, s := newTestApp(t)
	reply := serveThreadPosts(s)
	following := func(root string) string { return threadsPath(teamID) + "/" + root + "/following" }
	s.JSON(http.MethodPut, following(rootOpen), 200, map[string]string{"status": "OK"})
	s.JSON(http.MethodDelete, following(rootOpen), 200, map[string]string{"status": "OK"})
	s.JSON(http.MethodPut, following(rootDM), 200, map[string]string{"status": "OK"})

	r := run(app, "thread", "follow", reply, rootDM)
	mustCode(t, r, 0)
	if r.out != "Following: "+rootOpen+"\nFollowing: "+rootDM+"\n" {
		t.Fatalf("follow output: %q", r.out)
	}
	if s.Count(http.MethodPut, following(rootOpen)) != 1 || s.Count(http.MethodPut, following(rootDM)) != 1 {
		t.Fatal("follow requests not sent")
	}

	var got []actionResult
	mustJSON(t, run(app, "thread", "unfollow", rootOpen, reply, "--json"), &got)
	want := actionResult{Action: "unfollowed", ID: rootOpen}
	if len(got) != 2 || got[0] != want || got[1] != want {
		t.Fatalf("unfollow JSON: %+v", got)
	}
	// The channel of the root post is fetched once per call: once for
	// follow, once for unfollow of two posts in the same channel.
	if n := s.Count(http.MethodGet, "/api/v4/channels/"+townSq); n != 2 {
		t.Fatalf("channel fetched %d times", n)
	}
}

func TestThreadFollowFailure(t *testing.T) {
	app, s := newTestApp(t)
	serveThreadPosts(s)
	missing := fakemm.ID("missing")
	s.JSON(http.MethodPut, threadsPath(teamID)+"/"+rootOpen+"/following", 200, map[string]string{"status": "OK"})
	r := run(app, "thread", "follow", rootOpen, missing)
	mustCode(t, r, 5)
	if r.out != "Following: "+rootOpen+"\n" || !strings.Contains(r.err, missing) {
		t.Fatalf("partial failure: out %q err %q", r.out, r.err)
	}
}

func TestThreadFollowBadID(t *testing.T) {
	app, _ := newTestApp(t)
	mustCode(t, run(app, "thread", "follow", "not-an-id"), 2)
	mustCode(t, run(app, "thread", "unfollow"), 2)
}

func TestThreadRead(t *testing.T) {
	app, s := newTestApp(t)
	reply := serveThreadPosts(s)
	readPath := threadsPath(teamID) + "/" + rootOpen + "/read/" + strconv.FormatInt(now.UnixMilli(), 10)
	s.JSON(http.MethodPut, readPath, 200, &model.ThreadResponse{PostId: rootOpen})
	s.JSON(http.MethodPut, threadsPath(teamID)+"/read", 200, map[string]string{"status": "OK"})

	r := run(app, "thread", "read", reply)
	mustCode(t, r, 0)
	if r.out != "Marked read: "+rootOpen+"\n" || s.Count(http.MethodPut, readPath) != 1 {
		t.Fatalf("read: %q, requests %d", r.out, s.Count(http.MethodPut, readPath))
	}

	r = run(app, "thread", "read", "--all")
	mustCode(t, r, 0)
	if r.out != "Marked all threads read: myteam\n" {
		t.Fatalf("read --all: %q", r.out)
	}
	var got []actionResult
	mustJSON(t, run(app, "thread", "read", "--all", "--json"), &got)
	if len(got) != 1 || got[0] != (actionResult{Action: "marked_read", Team: "myteam"}) {
		t.Fatalf("read --all JSON: %+v", got)
	}
	if n := s.Count(http.MethodPut, threadsPath(teamID)+"/read"); n != 2 {
		t.Fatalf("read --all requests: %d", n)
	}
}

func TestThreadReadUsage(t *testing.T) {
	app, _ := newTestApp(t)
	mustCode(t, run(app, "thread", "read"), 2)
	mustCode(t, run(app, "thread", "read", "--all", rootOpen), 2)
	mustCode(t, run(app, "thread", "read", "bad"), 2)
}

// TestThreadHelp: the help alone tells an agent how to use the commands.
func TestThreadHelp(t *testing.T) {
	app, _ := newTestApp(t)
	for _, tc := range []struct {
		args []string
		want []string
	}{
		{[]string{"thread"}, []string{"root post", "does not mark", "post thread", "--reply", "--team"}},
		{[]string{"thread", "list"}, []string{"Examples:", "--json", "unread_replies", "last_reply_at", "channel_type", "post thread"}},
		{[]string{"thread", "follow"}, []string{"Examples:", "reply", "exit code"}},
		{[]string{"thread", "unfollow"}, []string{"Examples:", "reply", "exit code"}},
		{[]string{"thread", "read"}, []string{"Examples:", "--all", "marked_read", "exit code"}},
	} {
		r := run(app, append(tc.args, "--help")...)
		mustCode(t, r, 0)
		for _, w := range tc.want {
			if !strings.Contains(strings.ToLower(r.out), strings.ToLower(w)) {
				t.Errorf("%v --help lacks %q:\n%s", tc.args, w, r.out)
			}
		}
	}
}

func TestThreadChannelSelfDM(t *testing.T) {
	self := fakemm.ID("selfdm")
	channels := map[string]teamChannel{self: {ch: &model.Channel{Id: self, Name: meID + "__" + meID, Type: model.ChannelTypeDirect}}}
	addr, typ, label := threadChannel(channels, self, false, map[string]string{meID: "alice"}, meID)
	if addr != "@alice" || typ != "D" || label != "@alice" {
		t.Fatalf("got %q %q %q", addr, typ, label)
	}
}
