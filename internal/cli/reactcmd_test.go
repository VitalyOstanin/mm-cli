package cli

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"

	"github.com/VitalyOstanin/mm-cli/internal/clierr"
	"github.com/VitalyOstanin/mm-cli/internal/fakemm"
)

const reactionsPath = "/api/v4/reactions"

func unreactPath(post, emoji string) string {
	return "/api/v4/users/" + meID + "/posts/" + post + "/reactions/" + emoji
}

func serveReact(s *fakemm.Server) {
	s.Handle(http.MethodPost, reactionsPath, func(w http.ResponseWriter, _ *http.Request) {
		fakemm.WriteJSON(w, 200, &model.Reaction{})
	})
}

func TestPostReactionAddAdds(t *testing.T) {
	app, s := newTestApp(t)
	serveReact(s)
	p := fakemm.ID("p1")
	r := run(app, "post", "reaction", "add", p, "+1", ":white_check_mark:", "Eyes", ":+1:")
	mustCode(t, r, 0)
	if n := s.Count(http.MethodPost, reactionsPath); n != 3 {
		t.Fatalf("%d requests, want 3 (repeat of +1 is dropped)", n)
	}
	var got model.Reaction
	if err := json.Unmarshal(s.Last(http.MethodPost, reactionsPath).Body, &got); err != nil {
		t.Fatal(err)
	}
	if got.UserId != meID || got.PostId != p || got.EmojiName != "eyes" {
		t.Errorf("last reaction %+v, want alice, %s, eyes (lower-cased)", got, p)
	}
	want := "Reacted :+1: on " + p + "\nReacted :white_check_mark: on " + p + "\nReacted :eyes: on " + p + "\n"
	if r.out != want {
		t.Errorf("output %q, want %q", r.out, want)
	}
}

func TestPostReactionAddColonsStripped(t *testing.T) {
	app, s := newTestApp(t)
	serveReact(s)
	mustCode(t, run(app, "post", "reaction", "add", fakemm.ID("p1"), ":tada:"), 0)
	var got model.Reaction
	if err := json.Unmarshal(s.Last(http.MethodPost, reactionsPath).Body, &got); err != nil {
		t.Fatal(err)
	}
	if got.EmojiName != "tada" {
		t.Errorf("emoji %q, want tada", got.EmojiName)
	}
}

func TestPostReactionAddJSON(t *testing.T) {
	app, s := newTestApp(t)
	serveReact(s)
	p := fakemm.ID("p1")
	r := run(app, "post", "reaction", "add", p, "+1", "heart", "--json")
	mustCode(t, r, 0)
	var got []map[string]string
	if err := json.Unmarshal([]byte(r.out), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0]["action"] != "reaction_added" || got[0]["post_id"] != p || got[0]["emoji"] != "+1" || got[1]["emoji"] != "heart" || len(got[0]) != 3 {
		t.Errorf("json %s", r.out)
	}
}

func TestPostReactionAddInvalid(t *testing.T) {
	app, s := newTestApp(t)
	p := fakemm.ID("p1")
	for _, args := range [][]string{
		{"post", "reaction", "add", p, "bad name"},
		{"post", "reaction", "add", p, "+1", "a/b"},
		{"post", "reaction", "add", p, "::"},
		{"post", "reaction", "add", p, strings.Repeat("a", 65)},
		{"post", "reaction", "add", "short", "+1"},
		{"post", "reaction", "add", p},
		{"post", "reaction", "remove", p, "bad:name"},
		{"post", "reaction", "add", p, "+1", "heart", "--replace", "eyes"},
		{"post", "reaction", "add", p, "+1", "--replace", ":+1:"},
		{"post", "reaction", "add", p, "+1", "--replace", "bad name"},
	} {
		mustCode(t, run(app, args...), clierr.CodeUsage)
	}
	if s.Total() != 0 {
		t.Fatalf("invalid commands made %d requests", s.Total())
	}
}

func TestPostReactionAddServerErrors(t *testing.T) {
	for status, code := range map[int]int{404: clierr.CodeNotFound, 403: clierr.CodeForbidden} {
		app, s := newTestApp(t)
		s.Handle(http.MethodPost, reactionsPath, func(w http.ResponseWriter, _ *http.Request) {
			fakemm.AppError(w, status, "no")
		})
		mustCode(t, run(app, "post", "reaction", "add", fakemm.ID("p1"), "nope"), code)
	}
}

func TestPostReactionAddStopsAtFailure(t *testing.T) {
	app, s := newTestApp(t)
	s.Handle(http.MethodPost, reactionsPath, func(w http.ResponseWriter, r *http.Request) {
		var re model.Reaction
		_ = json.NewDecoder(r.Body).Decode(&re)
		if re.EmojiName == "custom" {
			fakemm.AppError(w, 404, "no emoji")
			return
		}
		fakemm.WriteJSON(w, 200, &re)
	})
	r := run(app, "post", "reaction", "add", fakemm.ID("p1"), "+1", "custom", "heart", "--json")
	mustCode(t, r, clierr.CodeNotFound)
	if !strings.Contains(r.out, `"emoji": "+1"`) && !strings.Contains(r.out, `"emoji":"+1"`) {
		t.Errorf("done list not printed: %q", r.out)
	}
	if !strings.Contains(r.err, ":custom:") || s.Count(http.MethodPost, reactionsPath) != 2 {
		t.Errorf("err %q, requests %d", r.err, s.Count(http.MethodPost, reactionsPath))
	}
}

func TestPostReactionRemove(t *testing.T) {
	app, s := newTestApp(t)
	p := fakemm.ID("p1")
	for _, e := range []string{"+1", "heart"} {
		s.JSON(http.MethodDelete, unreactPath(p, e), 200, map[string]string{"status": "OK"})
	}
	r := run(app, "post", "reaction", "remove", p, ":+1:", "heart", "--json")
	mustCode(t, r, 0)
	var got []map[string]string
	if err := json.Unmarshal([]byte(r.out), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0]["action"] != "reaction_removed" || got[0]["emoji"] != "+1" || got[1]["post_id"] != p {
		t.Errorf("json %s", r.out)
	}
	r = run(app, "post", "reaction", "remove", p, "heart")
	mustCode(t, r, 0)
	if r.out != "Removed :heart: from "+p+"\n" {
		t.Errorf("output %q", r.out)
	}
	// A reaction that is not there is the server's success: nothing is checked first.
	s.JSON(http.MethodDelete, unreactPath(p, "eyes"), 200, map[string]string{"status": "OK"})
	mustCode(t, run(app, "post", "reaction", "remove", p, "eyes"), 0)
}

func TestPostReactionRemoveErrors(t *testing.T) {
	app, s := newTestApp(t)
	p := fakemm.ID("p1")
	s.Handle(http.MethodDelete, unreactPath(p, "a"), func(w http.ResponseWriter, _ *http.Request) { fakemm.AppError(w, 404, "no") })
	s.Handle(http.MethodDelete, unreactPath(p, "b"), func(w http.ResponseWriter, _ *http.Request) { fakemm.AppError(w, 403, "no") })
	mustCode(t, run(app, "post", "reaction", "remove", p, "a"), clierr.CodeNotFound)
	mustCode(t, run(app, "post", "reaction", "remove", p, "b"), clierr.CodeForbidden)
}

func TestPostReactionAddReplace(t *testing.T) {
	app, s := newTestApp(t)
	serveReact(s)
	p := fakemm.ID("p1")
	s.JSON(http.MethodDelete, unreactPath(p, "eyes"), 200, map[string]string{"status": "OK"})
	r := run(app, "post", "reaction", "add", p, "heart", "--replace", ":eyes:", "--json")
	mustCode(t, r, 0)
	var got []map[string]string
	if err := json.Unmarshal([]byte(r.out), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0]["action"] != "reaction_replaced" || got[0]["emoji"] != "heart" || got[0]["old_emoji"] != "eyes" || got[0]["post_id"] != p {
		t.Errorf("json %s", r.out)
	}
	r = run(app, "post", "reaction", "add", p, "heart", "--replace", "eyes")
	mustCode(t, r, 0)
	if r.out != "Replaced :eyes: with :heart: on "+p+"\n" {
		t.Errorf("output %q", r.out)
	}
}

func TestPostReactionAddReplaceAddFailsKeepsOld(t *testing.T) {
	app, s := newTestApp(t)
	p := fakemm.ID("p1")
	s.Handle(http.MethodPost, reactionsPath, func(w http.ResponseWriter, _ *http.Request) { fakemm.AppError(w, 404, "no emoji") })
	s.JSON(http.MethodDelete, unreactPath(p, "eyes"), 200, map[string]string{"status": "OK"})
	r := run(app, "post", "reaction", "add", p, "nope", "--replace", "eyes")
	mustCode(t, r, clierr.CodeNotFound)
	if n := s.Count(http.MethodDelete, unreactPath(p, "eyes")); n != 0 {
		t.Errorf("old reaction removed %d times although adding failed", n)
	}
}

func TestPostReactionAddReplaceDeleteFails(t *testing.T) {
	app, s := newTestApp(t)
	serveReact(s)
	p := fakemm.ID("p1")
	s.Handle(http.MethodDelete, unreactPath(p, "eyes"), func(w http.ResponseWriter, _ *http.Request) { fakemm.AppError(w, 403, "no") })
	r := run(app, "post", "reaction", "add", p, "heart", "--replace", "eyes", "--json")
	mustCode(t, r, clierr.CodeForbidden)
	if !strings.Contains(r.err, "new reaction was added") {
		t.Errorf("err %q does not say the new reaction was added", r.err)
	}
	if !strings.Contains(r.out, "reaction_added") || strings.Contains(r.out, "reaction_replaced") {
		t.Errorf("out %q must list the added reaction only", r.out)
	}
}

func serveOnePost(s *fakemm.Server, p string, reactions []*model.Reaction) {
	s.JSON(http.MethodGet, "/api/v4/posts/"+p, 200, &model.Post{Id: p, ChannelId: townSq, UserId: bobID, Message: "hello", CreateAt: now.UnixMilli()})
	s.JSON(http.MethodGet, "/api/v4/posts/"+p+"/reactions", 200, reactions)
	s.JSON(http.MethodGet, "/api/v4/channels/"+townSq, 200, &model.Channel{Id: townSq, TeamId: teamID, Name: "town-square", Type: model.ChannelTypeOpen})
	s.JSON(http.MethodGet, "/api/v4/teams/"+teamID, 200, &model.Team{Id: teamID, Name: "myteam"})
	s.JSON(http.MethodPost, "/api/v4/users/ids", 200, []*model.User{{Id: meID, Username: "alice"}, {Id: bobID, Username: "bob"}})
}

func TestPostReactionListOfOnePost(t *testing.T) {
	app, s := newTestApp(t)
	p := fakemm.ID("p1")
	serveOnePost(s, p, []*model.Reaction{
		reaction(p, bobID, "heart", time.Minute),
		reaction(p, meID, "+1", 5*time.Minute),
	})
	r := run(app, "post", "reaction", "list", p, "--json")
	mustCode(t, r, 0)
	var got []map[string]string
	if err := json.Unmarshal([]byte(r.out), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0]["user"] != "alice" || got[0]["emoji"] != "+1" || got[1]["user"] != "bob" || got[1]["emoji"] != "heart" {
		t.Fatalf("json (oldest first, mine included) %s", r.out)
	}
	if got[0]["channel"] != "town-square" || got[0]["team"] != "myteam" || got[0]["message"] != "hello" || got[0]["permalink"] != s.URL+"/myteam/pl/"+p {
		t.Errorf("record %v", got[0])
	}
	if n := s.Count(http.MethodGet, "/api/v4/posts/"+p+"/reactions"); n != 1 {
		t.Errorf("%d reaction requests, want 1", n)
	}
	r = run(app, "post", "reaction", "list", p, "-n", "1", "--json")
	mustCode(t, r, 0)
	if strings.Contains(r.out, `"+1"`) || !strings.Contains(r.out, "heart") {
		t.Errorf("-n 1 should keep the newest: %s", r.out)
	}
	mustCode(t, run(app, "post", "reaction", "list", p), 0)
}

func TestPostReactionListOfOnePostEmpty(t *testing.T) {
	app, s := newTestApp(t)
	p := fakemm.ID("p1")
	serveOnePost(s, p, nil)
	r := run(app, "post", "reaction", "list", p, "--json")
	mustCode(t, r, 0)
	if strings.TrimSpace(r.out) != "[]" {
		t.Errorf("out %q, want []", r.out)
	}
}

func TestPostReactionListPostUsage(t *testing.T) {
	app, s := newTestApp(t)
	p := fakemm.ID("p1")
	mustCode(t, run(app, "post", "reaction", "list", p, "--since", "2d"), clierr.CodeUsage)
	mustCode(t, run(app, "post", "reaction", "list", "bad"), clierr.CodeUsage)
	mustCode(t, run(app, "post", "reaction", "list", p, "-n", "0"), clierr.CodeUsage)
	if s.Total() != 0 {
		t.Fatalf("%d requests for invalid usage", s.Total())
	}
	s.Handle(http.MethodGet, "/api/v4/posts/"+p, func(w http.ResponseWriter, _ *http.Request) { fakemm.AppError(w, 404, "no") })
	mustCode(t, run(app, "post", "reaction", "list", p), clierr.CodeNotFound)
}

func TestMessagesShowReactions(t *testing.T) {
	app, s := newTestApp(t)
	s.JSON(http.MethodGet, "/api/v4/teams/name/myteam/channels/name/town-square", 200, &model.Channel{Id: townSq, Name: "town-square"})
	p1, p2 := fakemm.ID("p1"), fakemm.ID("p2")
	posts := &model.PostList{Order: []string{p1, p2}, Posts: map[string]*model.Post{
		p1: {Id: p1, UserId: meID, ChannelId: townSq, Message: "first", CreateAt: now.Add(-2 * time.Minute).UnixMilli(), Metadata: &model.PostMetadata{Reactions: []*model.Reaction{
			reaction(p1, bobID, "heart", 30*time.Second),
			reaction(p1, meID, "+1", time.Minute),
			reaction(p1, bobID, "+1", 40*time.Second),
		}}},
		p2: {Id: p2, UserId: bobID, ChannelId: townSq, Message: "second", CreateAt: now.Add(-time.Minute).UnixMilli()},
	}}
	s.JSON(http.MethodGet, "/api/v4/channels/"+townSq+"/posts", 200, posts)
	s.JSON(http.MethodPost, "/api/v4/users/ids", 200, []*model.User{{Id: meID, Username: "alice"}, {Id: bobID, Username: "bob"}})
	r := run(app, "post", "list", "town-square", "--json")
	mustCode(t, r, 0)
	var got []map[string]any
	if err := json.Unmarshal([]byte(r.out), &got); err != nil {
		t.Fatal(err)
	}
	want := `[{"count":2,"emoji":"+1","users":["alice","bob"]},{"count":1,"emoji":"heart","users":["bob"]}]`
	b, _ := json.Marshal(got[0]["reactions"])
	if string(b) != want {
		t.Errorf("reactions %s, want %s", b, want)
	}
	if _, ok := got[1]["reactions"]; ok {
		t.Errorf("reactions must be omitted without reactions: %v", got[1])
	}
	r = run(app, "post", "list", "town-square")
	mustCode(t, r, 0)
	if !strings.Contains(r.out, "\n  👍 :+1: 2  ❤️ :heart: 1\n") {
		t.Errorf("text %q lacks the reactions line", r.out)
	}
	if strings.Count(r.out, ":heart:") != 1 {
		t.Errorf("reactions line only for the message that has them: %q", r.out)
	}
}
