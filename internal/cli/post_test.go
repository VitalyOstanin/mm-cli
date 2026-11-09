package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"

	"github.com/VitalyOstanin/mm-cli/internal/clierr"
	"github.com/VitalyOstanin/mm-cli/internal/fakemm"
	"github.com/VitalyOstanin/mm-cli/internal/render"
)

var (
	selfDM = fakemm.ID("selfdm")
	townSq = fakemm.ID("townsq")
)

func serveSend(t *testing.T, s *fakemm.Server) *[]string {
	t.Helper()
	s.Handle(http.MethodPost, "/api/v4/channels/direct", func(w http.ResponseWriter, r *http.Request) {
		var ids []string
		_ = json.NewDecoder(r.Body).Decode(&ids)
		if len(ids) != 2 || ids[0] != meID || ids[1] != meID {
			fakemm.AppError(w, 400, "unexpected members")
			return
		}
		fakemm.WriteJSON(w, 201, &model.Channel{Id: selfDM, Type: model.ChannelTypeDirect})
	})
	var mu sync.Mutex
	var names []string
	s.Handle(http.MethodPost, "/api/v4/files", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil || r.FormValue("channel_id") != selfDM {
			fakemm.AppError(w, 400, "bad upload")
			return
		}
		_, fh, err := r.FormFile("files")
		if err != nil {
			fakemm.AppError(w, 400, "no file")
			return
		}
		mu.Lock()
		names = append(names, fh.Filename)
		id := fakemm.ID(fmt.Sprintf("file%d", len(names)))
		mu.Unlock()
		fakemm.WriteJSON(w, 201, &model.FileUploadResponse{FileInfos: []*model.FileInfo{{Id: id, Name: fh.Filename}}})
	})
	s.Handle(http.MethodPost, "/api/v4/posts", func(w http.ResponseWriter, r *http.Request) {
		var p model.Post
		_ = json.NewDecoder(r.Body).Decode(&p)
		p.Id = fakemm.ID("post")
		p.UserId = meID
		p.CreateAt = now.UnixMilli()
		fakemm.WriteJSON(w, 201, &p)
	})
	return &names
}

func writeFiles(t *testing.T, names ...string) []string {
	t.Helper()
	dir := t.TempDir()
	var out []string
	for _, n := range names {
		p := filepath.Join(dir, n)
		if err := os.WriteFile(p, []byte("content of "+n), 0o600); err != nil {
			t.Fatal(err)
		}
		out = append(out, p)
	}
	return out
}

func TestSendFilesToSelf(t *testing.T) {
	app, s := newTestApp(t)
	uploaded := serveSend(t, s)
	files := writeFiles(t, "a.txt", "b.pdf")
	r := run(app, "send", "@me", "two files", "-f", files[0], "-f", files[1])
	mustCode(t, r, 0)
	if r.out != "Sent: "+fakemm.ID("post")+"\n" {
		t.Fatal(r.out)
	}
	if strings.Join(*uploaded, ",") != "a.txt,b.pdf" {
		t.Fatalf("uploaded %v", *uploaded)
	}
	var sent model.Post
	_ = json.Unmarshal(s.Last(http.MethodPost, "/api/v4/posts").Body, &sent)
	if sent.ChannelId != selfDM || sent.Message != "two files" || len(sent.FileIds) != 2 ||
		sent.FileIds[0] != fakemm.ID("file1") || sent.FileIds[1] != fakemm.ID("file2") {
		t.Fatalf("%+v", &sent)
	}
	r = run(app, "post", "create", "@me", "again", "--json")
	mustCode(t, r, 0)
	var got render.Post
	if err := json.Unmarshal([]byte(r.out), &got); err != nil || got.Message != "again" || got.User.Username != "alice" {
		t.Fatalf("%q %v", r.out, err)
	}
	if s.Count(http.MethodPost, "/api/v4/channels/direct") != 1 {
		t.Fatal("the self DM must be resolved once and then cached")
	}
}

func TestSendStdinAndErrors(t *testing.T) {
	app, s := newTestApp(t)
	serveSend(t, s)
	mustCode(t, runStdin(app, "line 1\nline 2\n", "send", "@me", "-"), 0)
	var sent model.Post
	_ = json.Unmarshal(s.Last(http.MethodPost, "/api/v4/posts").Body, &sent)
	if sent.Message != "line 1\nline 2" {
		t.Fatalf("%q", sent.Message)
	}
	before := s.Total()
	mustCode(t, run(app, "send", "@me"), clierr.CodeUsage)
	mustCode(t, run(app, "send", "@me", "x", "-f", "/nonexistent/file"), clierr.CodeUsage)
	args := []string{"send", "@me"}
	for _, f := range writeFiles(t, "1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11") {
		args = append(args, "-f", f)
	}
	mustCode(t, run(app, args...), clierr.CodeUsage)
	mustCode(t, run(app, "send", "@me", "x", "--reply", "bad"), clierr.CodeUsage)
	mustCode(t, run(app, "send", "Town Square", "x"), clierr.CodeUsage)
	if s.Total() != before {
		t.Fatal("invalid commands must not reach the server")
	}
}

func servePosts(s *fakemm.Server) {
	s.JSON(http.MethodGet, "/api/v4/teams/name/myteam/channels/name/town-square", 200, &model.Channel{Id: townSq, Name: "town-square"})
	posts := &model.PostList{Posts: map[string]*model.Post{
		"p1": {Id: fakemm.ID("p1"), UserId: meID, ChannelId: townSq, Message: "first", CreateAt: now.Add(-3 * 60e9).UnixMilli()},
		"p2": {Id: fakemm.ID("p2"), UserId: bobID, ChannelId: townSq, Message: "second", CreateAt: now.Add(-2 * 60e9).UnixMilli()},
		"p3": {Id: fakemm.ID("p3"), UserId: bobID, ChannelId: townSq, Message: "third", CreateAt: now.Add(-60e9).UnixMilli()},
		"p4": {Id: fakemm.ID("p4"), UserId: bobID, ChannelId: townSq, Message: "deleted", CreateAt: now.UnixMilli(), DeleteAt: 1},
	}}
	s.JSON(http.MethodGet, "/api/v4/channels/"+townSq+"/posts", 200, posts)
	s.JSON(http.MethodPost, "/api/v4/users/ids", 200, []*model.User{{Id: meID, Username: "alice"}, {Id: bobID, Username: "bob"}})
}

func TestList(t *testing.T) {
	app, s := newTestApp(t)
	servePosts(s)
	r := run(app, "post", "list", "town-square", "-n", "2")
	mustCode(t, r, 0)
	want := "2026-03-04 11:58  @bob  id " + fakemm.ID("p2") + "\nsecond\n\n2026-03-04 11:59  @bob  id " + fakemm.ID("p3") + "\nthird\n"
	if r.out != want {
		t.Fatalf("got\n%s\nwant\n%s", r.out, want)
	}
	if q := s.Last(http.MethodGet, "/api/v4/channels/"+townSq+"/posts").Query; q.Get("per_page") != "2" {
		t.Fatalf("query %v", q)
	}
	r = run(app, "messages", "myteam/town-square", "--json")
	mustCode(t, r, 0)
	var got []render.Post
	if err := json.Unmarshal([]byte(r.out), &got); err != nil || len(got) != 3 || got[0].User.Username != "alice" {
		t.Fatalf("%q %v", r.out, err)
	}
	if s.Count(http.MethodPost, "/api/v4/users/ids") != 1 {
		t.Fatal("names must be fetched once and then cached")
	}
	r = run(app, "post", "list", "town-square", "--since", "150s")
	mustCode(t, r, 0)
	if strings.Contains(r.out, "first") || !strings.Contains(r.out, "second") || !strings.Contains(r.out, "third") {
		t.Fatalf("--since 150s:\n%s", r.out)
	}
	mustCode(t, run(app, "post", "list", "town-square", "--since", "2h"), 0)
	if q := s.Last(http.MethodGet, "/api/v4/channels/"+townSq+"/posts").Query; q.Has("since") || q.Get("per_page") != "20" {
		t.Fatalf("since query %v", q)
	}
	mustCode(t, run(app, "post", "list", "town-square", "--before", fakemm.ID("p3")), 0)
	if q := s.Last(http.MethodGet, "/api/v4/channels/"+townSq+"/posts").Query; q.Get("before") != fakemm.ID("p3") {
		t.Fatalf("before query %v", q)
	}
	mustCode(t, run(app, "post", "list", "town-square", "--since", "2h", "--before", fakemm.ID("p3")), clierr.CodeUsage)
	mustCode(t, run(app, "post", "list", "town-square", "-n", "0"), clierr.CodeUsage)
	mustCode(t, run(app, "post", "list", "town-square", "-n", "201"), clierr.CodeUsage)
	mustCode(t, run(app, "post", "list", "missing"), clierr.CodeNotFound)
}

func TestGetEditDelete(t *testing.T) {
	app, s := newTestApp(t)
	id := fakemm.ID("p1")
	s.JSON(http.MethodGet, "/api/v4/posts/"+id, 200, &model.Post{Id: id, UserId: bobID, Message: "hello", CreateAt: now.UnixMilli()})
	s.JSON(http.MethodPost, "/api/v4/users/ids", 200, []*model.User{{Id: bobID, Username: "bob"}})
	r := run(app, "post", "get", id)
	mustCode(t, r, 0)
	if !strings.Contains(r.out, "@bob") || !strings.Contains(r.out, "hello") {
		t.Fatal(r.out)
	}
	r = run(app, "post", "get", id, "--json")
	var got render.Post
	if err := json.Unmarshal([]byte(r.out), &got); err != nil || got.ID != id {
		t.Fatalf("%q %v", r.out, err)
	}
	s.Handle(http.MethodPut, "/api/v4/posts/"+id+"/patch", func(w http.ResponseWriter, r *http.Request) {
		var patch model.PostPatch
		_ = json.NewDecoder(r.Body).Decode(&patch)
		fakemm.WriteJSON(w, 200, &model.Post{Id: id, Message: *patch.Message, EditAt: 1})
	})
	r = run(app, "post", "edit", id, "new text")
	mustCode(t, r, 0)
	if r.out != "Edited: "+id+"\n" {
		t.Fatal(r.out)
	}
	mustCode(t, run(app, "post", "edit", id, ""), clierr.CodeUsage)
	id2 := fakemm.ID("p2")
	s.JSON(http.MethodDelete, "/api/v4/posts/"+id, 200, map[string]string{"status": "OK"})
	s.Handle(http.MethodDelete, "/api/v4/posts/"+id2, func(w http.ResponseWriter, _ *http.Request) {
		fakemm.AppError(w, http.StatusForbidden, "not your post")
	})
	r = run(app, "post", "delete", id, id2, "--yes")
	mustCode(t, r, clierr.CodeForbidden)
	if r.out != "Deleted: "+id+"\n" || !strings.Contains(r.err, id2) {
		t.Fatalf("out %q err %q", r.out, r.err)
	}
}

func TestChannelCreate(t *testing.T) {
	app, s := newTestApp(t)
	teamID := fakemm.ID("myteam")
	s.JSON(http.MethodGet, "/api/v4/teams/name/myteam", 200, &model.Team{Id: teamID, Name: "myteam"})
	s.Handle(http.MethodPost, "/api/v4/channels", func(w http.ResponseWriter, r *http.Request) {
		var ch model.Channel
		_ = json.NewDecoder(r.Body).Decode(&ch)
		if ch.TeamId != teamID || ch.Type != model.ChannelTypePrivate || ch.Name != "notes" {
			fakemm.AppError(w, 400, "bad channel")
			return
		}
		ch.Id = fakemm.ID("notes")
		fakemm.WriteJSON(w, 201, &ch)
	})
	r := run(app, "channel", "create", "notes", "--private", "--exist-ok")
	mustCode(t, r, 0)
	if !strings.HasPrefix(r.out, "Created: myteam/notes") {
		t.Fatal(r.out)
	}
	s.JSON(http.MethodGet, "/api/v4/teams/name/myteam/channels/name/notes", 200, &model.Channel{Id: fakemm.ID("notes"), Name: "notes"})
	r = run(app, "channel", "create", "notes", "--private", "--exist-ok")
	mustCode(t, r, 0)
	if !strings.HasPrefix(r.out, "Exists: myteam/notes") || s.Count(http.MethodPost, "/api/v4/channels") != 1 {
		t.Fatal(r.out)
	}
	mustCode(t, run(app, "channel", "create", "Bad Name"), clierr.CodeUsage)
}

func TestSearchTimeZoneOffsetInSeconds(t *testing.T) {
	for _, tc := range []struct {
		name   string
		offset int
	}{
		{"UTC+3", 3 * 3600},
		{"UTC+5:30", 5*3600 + 1800},
		{"UTC-9:30", -(9*3600 + 1800)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, s := newTestApp(t)
			serveTownSquare(s)
			app.Location = time.FixedZone(tc.name, tc.offset)
			s.JSON(http.MethodPost, "/api/v4/teams/"+teamID+"/posts/search", 200, &model.PostList{})
			mustCode(t, run(app, "post", "search", "deploy"), 0)
			var p model.SearchParameter
			if err := json.Unmarshal(s.Last(http.MethodPost, "/api/v4/teams/"+teamID+"/posts/search").Body, &p); err != nil {
				t.Fatal(err)
			}
			if p.TimeZoneOffset == nil || *p.TimeZoneOffset != tc.offset {
				got := "none"
				if p.TimeZoneOffset != nil {
					got = fmt.Sprint(*p.TimeZoneOffset)
				}
				t.Fatalf("timezone_offset %s, want %d seconds", got, tc.offset)
			}
		})
	}
}

// The server's "since" filter selects posts by update time and has no page
// size, so --since must read one bounded page and filter by creation time.
func TestListSinceBoundedByCreateAt(t *testing.T) {
	app, s := newTestApp(t)
	serveTownSquare(s)
	at := func(d time.Duration) int64 { return now.Add(-d).UnixMilli() }
	s.Handle(http.MethodGet, "/api/v4/channels/"+townSq+"/posts", func(w http.ResponseWriter, r *http.Request) {
		pl := &model.PostList{Posts: map[string]*model.Post{
			"edited": {Id: fakemm.ID("edited"), UserId: bobID, Message: "old but edited", CreateAt: at(5 * time.Hour), UpdateAt: at(time.Minute)},
			"old":    {Id: fakemm.ID("old"), UserId: bobID, Message: "old", CreateAt: at(3 * time.Hour), UpdateAt: at(3 * time.Hour)},
			"new1":   {Id: fakemm.ID("new1"), UserId: bobID, Message: "new one", CreateAt: at(time.Hour), UpdateAt: at(time.Hour)},
			"new2":   {Id: fakemm.ID("new2"), UserId: bobID, Message: "new two", CreateAt: at(30 * time.Minute), UpdateAt: at(30 * time.Minute)},
		}}
		fakemm.WriteJSON(w, 200, pl)
	})
	r := run(app, "post", "list", "town-square", "--since", "2h", "-n", "5")
	mustCode(t, r, 0)
	if strings.Contains(r.out, "old") || !strings.Contains(r.out, "new one") || !strings.Contains(r.out, "new two") {
		t.Fatalf("only messages created in the last 2h expected:\n%s", r.out)
	}
	q := s.Last(http.MethodGet, "/api/v4/channels/"+townSq+"/posts").Query
	if q.Has("since") || q.Get("per_page") != "5" {
		t.Fatalf("want one bounded page, got query %v", q)
	}
	if c := s.Count(http.MethodGet, "/api/v4/channels/"+townSq+"/posts"); c != 1 {
		t.Fatalf("%d requests, want 1", c)
	}
}
