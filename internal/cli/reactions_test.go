package cli

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"

	"github.com/VitalyOstanin/mm-cli/internal/clierr"
	"github.com/VitalyOstanin/mm-cli/internal/fakemm"
)

var (
	reactCarol = fakemm.ID("carol")
	reactDev   = fakemm.ID("dev")
	reactDM    = fakemm.ID("dm")
)

func reactionPost(id, channel string, ago time.Duration, reactions ...*model.Reaction) *model.Post {
	return &model.Post{
		Id: id, ChannelId: channel, UserId: meID, Message: "text of " + id,
		CreateAt: now.Add(-ago).UnixMilli(), HasReactions: len(reactions) > 0,
		Metadata: &model.PostMetadata{Reactions: reactions},
	}
}

func reaction(post, user, emoji string, ago time.Duration) *model.Reaction {
	return &model.Reaction{PostId: post, UserId: user, EmojiName: emoji, CreateAt: now.Add(-ago).UnixMilli()}
}

// serveReactions serves two teams, three channels (a channel of each team and
// a direct one with bob) and the given search result.
func serveReactions(s *fakemm.Server, posts ...*model.Post) {
	pl := &model.PostList{Posts: map[string]*model.Post{}}
	for _, p := range posts {
		pl.Order = append(pl.Order, p.Id)
		pl.Posts[p.Id] = p
	}
	s.JSON(http.MethodPost, "/api/v4/posts/search", 200, pl)
	s.JSON(http.MethodGet, "/api/v4/users/"+meID+"/teams", 200, []*model.Team{{Id: teamID, Name: "myteam"}, {Id: team2ID, Name: "team2"}})
	s.JSON(http.MethodGet, "/api/v4/users/"+meID+"/channels", 200, []*model.Channel{
		{Id: townSq, TeamId: teamID, Name: "town-square", Type: model.ChannelTypeOpen},
		{Id: reactDev, TeamId: team2ID, Name: "dev", Type: model.ChannelTypePrivate},
		{Id: reactDM, Name: meID + "__" + bobID, Type: model.ChannelTypeDirect},
	})
	s.JSON(http.MethodPost, "/api/v4/users/ids", 200, []*model.User{
		{Id: meID, Username: "alice"}, {Id: bobID, Username: "bob"}, {Id: reactCarol, Username: "carol"},
	})
}

func TestPostReactions(t *testing.T) {
	app, s := newTestApp(t)
	p1, p2, p3 := fakemm.ID("p1"), fakemm.ID("p2"), fakemm.ID("p3")
	foreign := reactionPost(fakemm.ID("p4"), townSq, time.Hour, reaction(fakemm.ID("p4"), reactCarol, "eyes", time.Minute))
	foreign.UserId = bobID
	serveReactions(s,
		reactionPost(p1, townSq, 2*time.Hour,
			reaction(p1, bobID, "thumbsup", 30*time.Minute),
			reaction(p1, meID, "tada", 40*time.Minute)),
		reactionPost(p2, reactDev, 3*time.Hour, reaction(p2, reactCarol, "heart", 50*time.Minute)),
		reactionPost(p3, reactDM, 4*time.Hour, reaction(p3, bobID, "ok", 10*time.Minute)),
		reactionPost(fakemm.ID("p5"), townSq, 5*time.Hour),
		reactionPost(fakemm.ID("p6"), townSq, 200*time.Hour, reaction(fakemm.ID("p6"), bobID, "old", time.Minute)),
		foreign,
	)
	// Decode into maps to check the exact JSON keys.
	r := run(app, "post", "reaction", "list", "--json")
	mustCode(t, r, 0)
	var raw []map[string]string
	if err := json.Unmarshal([]byte(r.out), &raw); err != nil {
		t.Fatal(err)
	}
	if len(raw) != 3 {
		t.Fatalf("want 3 reactions (others', recent, own messages), got %d:\n%s", len(raw), r.out)
	}
	// Oldest reaction first: carol 50m ago, bob 30m, bob 10m.
	want := []map[string]string{
		{"post_id": p2, "root_id": "", "channel_id": reactDev, "channel": "dev", "channel_display_name": "dev", "team": "team2", "user_id": reactCarol, "user": "carol", "emoji": "heart",
			"create_at": "2026-03-04T11:10:00Z", "message": "text of " + p2},
		{"post_id": p1, "root_id": "", "channel_id": townSq, "channel": "town-square", "channel_display_name": "town-square", "team": "myteam", "user_id": bobID, "user": "bob", "emoji": "thumbsup",
			"create_at": "2026-03-04T11:30:00Z", "message": "text of " + p1},
		{"post_id": p3, "root_id": "", "channel_id": reactDM, "channel": "@bob", "channel_display_name": "@bob", "team": "myteam", "user_id": bobID, "user": "bob", "emoji": "ok",
			"create_at": "2026-03-04T11:50:00Z", "message": "text of " + p3},
	}
	for i, w := range want {
		w["permalink"] = s.URL + "/" + w["team"] + "/pl/" + w["post_id"]
		w["app_link"] = "mattermost://" + strings.TrimPrefix(s.URL, "http://") + "/" + w["team"] + "/pl/" + w["post_id"]
		for k, v := range w {
			if raw[i][k] != v {
				t.Errorf("reaction %d %s = %q, want %q", i, k, raw[i][k], v)
			}
		}
		if len(raw[i]) != len(w) {
			t.Errorf("reaction %d has %d fields, want %d", i, len(raw[i]), len(w))
		}
	}
	// Teams, one cross-team search, channels, users: no per-post requests.
	if n := s.Count(http.MethodPost, "/api/v4/posts/search"); n != 1 {
		t.Errorf("%d searches, want 1", n)
	}
	if n := s.Total(); n != 4 {
		t.Errorf("%d requests, want 4 (teams, search, channels, users)", n)
	}
	var p model.SearchParameter
	if err := json.Unmarshal(s.Last(http.MethodPost, "/api/v4/posts/search").Body, &p); err != nil {
		t.Fatal(err)
	}
	if p.Terms == nil || *p.Terms != "from:alice after:2026-02-24" {
		t.Errorf("search terms %v, want from:alice after:2026-02-24", p.Terms)
	}
}

func TestPostReactionsText(t *testing.T) {
	app, s := newTestApp(t)
	p1 := fakemm.ID("p1")
	post := reactionPost(p1, townSq, time.Hour, reaction(p1, bobID, "thumbsup", 30*time.Minute))
	post.Message = strings.Repeat("word ", 40) + "\nsecond line"
	serveReactions(s, post)
	r := run(app, "post", "reaction", "list")
	mustCode(t, r, 0)
	host := strings.TrimPrefix(s.URL, "http://")
	for _, w := range []string{"2026-03-04 11:30  @bob  👍 :thumbsup:  town-square\n", "word word", "…\n", "mattermost://" + host + "/myteam/pl/" + p1 + "\n"} {
		if !strings.Contains(r.out, w) {
			t.Errorf("output lacks %q:\n%s", w, r.out)
		}
	}
	if strings.Contains(r.out, "second line") {
		t.Errorf("message not shortened:\n%s", r.out)
	}
	r = run(app, "post", "reaction", "list", "--truncate", "1000")
	if !strings.Contains(r.out, "second line") || strings.Contains(r.out, "second line\n\n") {
		t.Errorf("--truncate 1000 must show the whole message on one line:\n%s", r.out)
	}
}

func TestPostReactionsNumberKeepsNewest(t *testing.T) {
	app, s := newTestApp(t)
	p1 := fakemm.ID("p1")
	serveReactions(s, reactionPost(p1, townSq, 5*time.Hour,
		reaction(p1, bobID, "a", 3*time.Hour), reaction(p1, reactCarol, "b", 2*time.Hour), reaction(p1, bobID, "c", time.Hour)))
	var rs []struct {
		Emoji string `json:"emoji"`
	}
	mustJSON(t, run(app, "post", "reaction", "list", "-n", "2", "--json"), &rs)
	if len(rs) != 2 || rs[0].Emoji != "b" || rs[1].Emoji != "c" {
		t.Fatalf("got %+v, want the newest two, oldest first", rs)
	}
}

func TestPostReactionsEmpty(t *testing.T) {
	app, s := newTestApp(t)
	serveReactions(s, reactionPost(fakemm.ID("p1"), townSq, time.Hour))
	r := run(app, "post", "reaction", "list", "--json")
	mustCode(t, r, 0)
	if strings.TrimSpace(r.out) != "[]" {
		t.Fatalf("got %q, want []", r.out)
	}
	// Nobody reacted: no channel or user lookups are needed.
	if n := s.Total(); n != 2 {
		t.Errorf("%d requests, want 2 (teams, search)", n)
	}
	r = run(app, "post", "reaction", "list")
	mustCode(t, r, 0)
	if r.out != "" {
		t.Errorf("text output %q, want nothing", r.out)
	}
}

func TestPostReactionsUsage(t *testing.T) {
	app, s := newTestApp(t)
	serveReactions(s)
	for _, args := range [][]string{
		{"--since", "yesterday"}, {"--since", "-2d"}, {"-n", "0"}, {"-n", "201"},
	} {
		mustCode(t, run(app, append([]string{"post", "reaction", "list"}, args...)...), clierr.CodeUsage)
	}
	if n := s.Total(); n != 0 {
		t.Fatalf("usage errors must not reach the server, got %d requests", n)
	}
}

func TestPostReactionsTeam(t *testing.T) {
	app, s := newTestApp(t)
	serveTownSquare(s)
	p1 := fakemm.ID("p1")
	s.JSON(http.MethodPost, "/api/v4/teams/"+teamID+"/posts/search", 200, &model.PostList{
		Order: []string{p1}, Posts: map[string]*model.Post{p1: reactionPost(p1, townSq, time.Hour, reaction(p1, bobID, "ok", time.Minute))},
	})
	s.JSON(http.MethodGet, "/api/v4/users/"+meID+"/teams/"+teamID+"/channels", 200, []*model.Channel{
		{Id: townSq, TeamId: teamID, Name: "town-square", Type: model.ChannelTypeOpen},
	})
	var rs []struct{ Team, Channel string }
	mustJSON(t, run(app, "post", "reaction", "list", "--team", "myteam", "--json"), &rs)
	if len(rs) != 1 || rs[0].Team != "myteam" || rs[0].Channel != "town-square" {
		t.Fatalf("got %+v", rs)
	}
	if n := s.Count(http.MethodPost, "/api/v4/posts/search"); n != 0 {
		t.Errorf("--team must search that team only, got %d cross-team searches", n)
	}
	mustCode(t, run(app, "post", "reaction", "list", "--team", "nosuch"), clierr.CodeNotFound)
}

// A full page of results asks for the next one; the paging stops at a short
// page.
func TestPostReactionsPaging(t *testing.T) {
	app, s := newTestApp(t)
	serveReactions(s)
	p1 := fakemm.ID("p1")
	s.Handle(http.MethodPost, "/api/v4/posts/search", func(w http.ResponseWriter, r *http.Request) {
		var p model.SearchParameter
		_ = json.NewDecoder(r.Body).Decode(&p)
		pl := &model.PostList{Posts: map[string]*model.Post{}}
		if *p.Page == 0 {
			for i := range reactionSearchPage {
				id := fakemm.ID("q" + strings.Repeat("x", 3) + string(rune('a'+i/26)) + string(rune('a'+i%26)))
				pl.Order = append(pl.Order, id)
				pl.Posts[id] = reactionPost(id, townSq, time.Hour)
			}
		} else {
			pl.Order = []string{p1}
			pl.Posts[p1] = reactionPost(p1, townSq, time.Hour, reaction(p1, bobID, "ok", time.Minute))
		}
		fakemm.WriteJSON(w, 200, pl)
	})
	var rs []struct {
		PostID string `json:"post_id"`
	}
	mustJSON(t, run(app, "post", "reaction", "list", "--json"), &rs)
	if len(rs) != 1 || rs[0].PostID != p1 {
		t.Fatalf("got %+v", rs)
	}
	if n := s.Count(http.MethodPost, "/api/v4/posts/search"); n != 2 {
		t.Errorf("%d searches, want 2", n)
	}
}

// serveFullPages makes the search return full pages for pages below full and
// extra posts on the page after them.
func serveFullPages(s *fakemm.Server, full, extra int) {
	s.Handle(http.MethodPost, "/api/v4/posts/search", func(w http.ResponseWriter, r *http.Request) {
		var p model.SearchParameter
		_ = json.NewDecoder(r.Body).Decode(&p)
		n := 0
		if *p.Page < full {
			n = reactionSearchPage
		} else if *p.Page == full {
			n = extra
		}
		pl := &model.PostList{Posts: map[string]*model.Post{}}
		for i := 0; i < n; i++ {
			id := fakemm.ID("w" + strconv.Itoa(*p.Page*1000+i))
			pl.Order = append(pl.Order, id)
			pl.Posts[id] = reactionPost(id, townSq, time.Hour)
		}
		fakemm.WriteJSON(w, 200, pl)
	})
}

// Exactly the bound of own messages is not a truncation; one more is.
func TestPostReactionsTruncationWarning(t *testing.T) {
	for _, tc := range []struct {
		extra int
		warn  bool
	}{{0, false}, {1, true}} {
		app, s := newTestApp(t)
		serveReactions(s)
		serveFullPages(s, reactionSearchPages, tc.extra)
		r := run(app, "post", "reaction", "list", "--json")
		if got := strings.Contains(r.err, "warning"); got != tc.warn {
			t.Errorf("extra=%d: warning=%v, want %v; stderr %q", tc.extra, got, tc.warn, r.err)
		}
	}
}

// Names are looked up only for people who matter: the reactors and the peers
// of direct channels that hold a found reaction, not every direct partner.
func TestPostReactionsLooksUpOnlyNeededNames(t *testing.T) {
	app, s := newTestApp(t)
	p1 := fakemm.ID("p1")
	serveReactions(s, reactionPost(p1, townSq, 2*time.Hour, reaction(p1, reactCarol, "eyes", time.Minute)))
	r := run(app, "post", "reaction", "list", "--json")
	mustCode(t, r, 0)
	var ids []string
	if err := json.Unmarshal(s.Last(http.MethodPost, "/api/v4/users/ids").Body, &ids); err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != reactCarol {
		t.Errorf("looked up %v, want only the reactor %s (not the direct partner)", ids, reactCarol)
	}
	if !strings.Contains(r.out, `"channel": "town-square"`) && !strings.Contains(r.out, `"channel":"town-square"`) {
		t.Errorf("channel name missing:\n%s", r.out)
	}
}
