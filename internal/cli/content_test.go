package cli

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"

	"github.com/VitalyOstanin/mm-cli/internal/clierr"
	"github.com/VitalyOstanin/mm-cli/internal/fakemm"
)

var teamID = fakemm.ID("myteam")

func serveTownSquare(s *fakemm.Server) {
	s.JSON(http.MethodGet, "/api/v4/teams/name/myteam/channels/name/town-square", 200, &model.Channel{Id: townSq, Name: "town-square"})
	s.JSON(http.MethodGet, "/api/v4/teams/name/myteam", 200, &model.Team{Id: teamID, Name: "myteam"})
	s.JSON(http.MethodPost, "/api/v4/users/ids", 200, []*model.User{{Id: meID, Username: "alice"}, {Id: bobID, Username: "bob"}})
}

func TestChannelGet(t *testing.T) {
	app, s := newTestApp(t)
	serveTownSquare(s)
	s.JSON(http.MethodGet, "/api/v4/channels/"+townSq, 200, &model.Channel{Id: townSq, Name: "town-square", DisplayName: "Town Square",
		Type: model.ChannelTypeOpen, Header: "welcome", TotalMsgCount: 42})
	s.JSON(http.MethodGet, "/api/v4/channels/"+townSq+"/stats", 200, &model.ChannelStats{ChannelId: townSq, MemberCount: 7})
	r := run(app, "channel", "get", "town-square")
	mustCode(t, r, 0)
	for _, want := range []string{"type: open", "display name: Town Square", "header: welcome", "members: 7", "messages: 42"} {
		if !strings.Contains(r.out, want) {
			t.Fatalf("missing %q in\n%s", want, r.out)
		}
	}
	dm := fakemm.ID("dm")
	s.JSON(http.MethodGet, "/api/v4/channels/"+dm, 200, &model.Channel{Id: dm, Name: meID + "__" + bobID, Type: model.ChannelTypeDirect})
	s.JSON(http.MethodGet, "/api/v4/channels/"+dm+"/stats", 200, &model.ChannelStats{MemberCount: 2})
	r = run(app, "channel", "get", dm, "--json")
	mustCode(t, r, 0)
	var info channelInfo
	if err := json.Unmarshal([]byte(r.out), &info); err != nil || info.DisplayName != "@bob" || info.Type != "direct" {
		t.Fatalf("%q %v", r.out, err)
	}
}

func TestChannelList(t *testing.T) {
	app, s := newTestApp(t)
	serveTownSquare(s)
	dm := fakemm.ID("dm")
	s.JSON(http.MethodGet, "/api/v4/users/"+meID+"/teams/"+teamID+"/channels", 200, []*model.Channel{
		{Id: townSq, Name: "town-square", DisplayName: "Town Square", Type: model.ChannelTypeOpen, TotalMsgCount: 10},
		{Id: fakemm.ID("notes"), Name: "notes", DisplayName: "Notes", Type: model.ChannelTypePrivate, TotalMsgCount: 3},
		{Id: dm, Name: meID + "__" + bobID, Type: model.ChannelTypeDirect, TotalMsgCount: 5},
	})
	s.JSON(http.MethodGet, "/api/v4/users/"+meID+"/teams/"+teamID+"/channels/members", 200, []model.ChannelMember{
		{ChannelId: townSq, MsgCount: 8, MentionCount: 1},
		{ChannelId: fakemm.ID("notes"), MsgCount: 3},
		{ChannelId: dm, MsgCount: 5},
	})
	r := run(app, "channel", "list")
	mustCode(t, r, 0)
	if !strings.Contains(r.out, "@bob") || !strings.Contains(r.out, "myteam/town-square") || !strings.Contains(r.out, "myteam/notes") {
		t.Fatal(r.out)
	}
	r = run(app, "channel", "list", "--unread")
	mustCode(t, r, 0)
	if strings.Count(strings.TrimSpace(r.out), "\n") != 0 || !strings.Contains(r.out, "unread 2, mentions 1") {
		t.Fatal(r.out)
	}
	r = run(app, "channel", "list", "--type", "D", "--json")
	var got []channelInfo
	if err := json.Unmarshal([]byte(r.out), &got); err != nil || len(got) != 1 || got[0].DisplayName != "@bob" {
		t.Fatalf("%q %v", r.out, err)
	}
	mustCode(t, run(app, "channel", "list", "--type", "X"), clierr.CodeUsage)
}

func TestChannelEditArchiveRead(t *testing.T) {
	app, s := newTestApp(t)
	serveTownSquare(s)
	s.Handle(http.MethodPut, "/api/v4/channels/"+townSq+"/patch", func(w http.ResponseWriter, r *http.Request) {
		var raw map[string]any
		_ = json.NewDecoder(r.Body).Decode(&raw)
		if raw["header"] != "new header" || raw["purpose"] != nil {
			fakemm.AppError(w, 400, "unexpected patch")
			return
		}
		fakemm.WriteJSON(w, 200, &model.Channel{Id: townSq, Header: "new header"})
	})
	mustCode(t, run(app, "channel", "edit", "town-square", "--header", "new header"), 0)
	mustCode(t, run(app, "channel", "edit", "town-square"), clierr.CodeUsage)
	s.JSON(http.MethodDelete, "/api/v4/channels/"+townSq, 200, map[string]string{"status": "OK"})
	s.JSON(http.MethodPost, "/api/v4/channels/"+townSq+"/restore", 200, &model.Channel{Id: townSq})
	mustCode(t, run(app, "channel", "archive", "town-square"), 0)
	mustCode(t, run(app, "channel", "unarchive", "town-square"), 0)
	s.Handle(http.MethodPost, "/api/v4/channels/members/"+meID+"/view", func(w http.ResponseWriter, r *http.Request) {
		var v model.ChannelView
		_ = json.NewDecoder(r.Body).Decode(&v)
		if v.ChannelId != townSq {
			fakemm.AppError(w, 400, "wrong channel")
			return
		}
		fakemm.WriteJSON(w, 200, &model.ChannelViewResponse{Status: "OK"})
	})
	mustCode(t, run(app, "channel", "read", "town-square"), 0)
}

func TestThreadSearchHistory(t *testing.T) {
	app, s := newTestApp(t)
	serveTownSquare(s)
	root := fakemm.ID("root")
	s.JSON(http.MethodGet, "/api/v4/posts/"+root+"/thread", 200, &model.PostList{Posts: map[string]*model.Post{
		"r": {Id: root, UserId: meID, Message: "question", CreateAt: 1},
		"a": {Id: fakemm.ID("ans"), UserId: bobID, RootId: root, Message: "answer", CreateAt: 2},
	}})
	r := run(app, "post", "thread", root)
	mustCode(t, r, 0)
	if strings.Index(r.out, "question") > strings.Index(r.out, "answer") || !strings.Contains(r.out, "@bob") {
		t.Fatal(r.out)
	}
	s.Handle(http.MethodPost, "/api/v4/teams/"+teamID+"/posts/search", func(w http.ResponseWriter, r *http.Request) {
		var p map[string]any
		_ = json.NewDecoder(r.Body).Decode(&p)
		if p["terms"] != "deploy in:town-square from:bob" {
			fakemm.AppError(w, 400, "terms: "+p["terms"].(string))
			return
		}
		fakemm.WriteJSON(w, 200, &model.PostList{Posts: map[string]*model.Post{"x": {Id: fakemm.ID("x"), UserId: bobID, Message: "deploy done"}}})
	})
	r = run(app, "post", "search", "deploy", "--in", "town-square", "--from", "@bob")
	mustCode(t, r, 0)
	if !strings.Contains(r.out, "deploy done") {
		t.Fatal(r.out)
	}
	mustCode(t, run(app, "post", "search", "x", "--in", "@bob"), clierr.CodeUsage)
	id, v1 := fakemm.ID("p"), fakemm.ID("v1")
	s.JSON(http.MethodGet, "/api/v4/posts/"+id+"/edit_history", 200, []*model.Post{{Id: v1, UserId: meID, Message: "old text", CreateAt: 1, DeleteAt: 2}})
	r = run(app, "post", "history", id)
	mustCode(t, r, 0)
	if !strings.Contains(r.out, "old text") || !strings.Contains(r.out, v1) {
		t.Fatal(r.out)
	}
	s.JSON(http.MethodPost, "/api/v4/posts/"+id+"/restore/"+v1, 200, &model.Post{Id: id, Message: "old text"})
	r = run(app, "post", "restore", id, v1)
	mustCode(t, r, 0)
	if r.out != "Restored: "+id+"\n" {
		t.Fatal(r.out)
	}
}

func TestPinAndSaved(t *testing.T) {
	app, s := newTestApp(t)
	serveTownSquare(s)
	id := fakemm.ID("p")
	s.JSON(http.MethodPost, "/api/v4/posts/"+id+"/pin", 200, map[string]string{"status": "OK"})
	s.JSON(http.MethodPost, "/api/v4/posts/"+id+"/unpin", 200, map[string]string{"status": "OK"})
	mustCode(t, run(app, "pin", "add", id), 0)
	mustCode(t, run(app, "pin", "remove", id), 0)
	s.JSON(http.MethodGet, "/api/v4/channels/"+townSq+"/pinned", 200, &model.PostList{Posts: map[string]*model.Post{"p": {Id: id, UserId: bobID, Message: "pinned one", IsPinned: true}}})
	r := run(app, "pin", "list", "town-square")
	mustCode(t, r, 0)
	if !strings.Contains(r.out, "pinned one") || !strings.Contains(r.out, "pinned") {
		t.Fatal(r.out)
	}
	check := func(w http.ResponseWriter, r *http.Request) {
		var prefs model.Preferences
		_ = json.NewDecoder(r.Body).Decode(&prefs)
		if len(prefs) != 1 || prefs[0].Category != "flagged_post" || prefs[0].Name != id || prefs[0].UserId != meID {
			fakemm.AppError(w, 400, "bad preference")
			return
		}
		fakemm.WriteJSON(w, 200, map[string]string{"status": "OK"})
	}
	s.Handle(http.MethodPut, "/api/v4/users/"+meID+"/preferences", check)
	s.Handle(http.MethodPost, "/api/v4/users/"+meID+"/preferences/delete", check)
	mustCode(t, run(app, "saved", "add", id), 0)
	mustCode(t, run(app, "saved", "remove", id), 0)
	s.JSON(http.MethodGet, "/api/v4/users/"+meID+"/posts/flagged", 200, &model.PostList{Posts: map[string]*model.Post{"p": {Id: id, UserId: bobID, Message: "saved one"}}})
	r = run(app, "saved", "list", "-n", "5")
	mustCode(t, r, 0)
	if !strings.Contains(r.out, "saved one") || s.Last(http.MethodGet, "/api/v4/users/"+meID+"/posts/flagged").Query.Get("per_page") != "5" {
		t.Fatal(r.out)
	}
}

func TestFileListAndDownload(t *testing.T) {
	app, s := newTestApp(t)
	serveTownSquare(s)
	f1, f2 := fakemm.ID("f1"), fakemm.ID("f2")
	p1, p2 := fakemm.ID("p1"), fakemm.ID("p2")
	s.JSON(http.MethodGet, "/api/v4/channels/"+townSq+"/posts", 200, &model.PostList{Order: []string{fakemm.ID("p3"), p2, p1}, Posts: map[string]*model.Post{
		p1: {Id: p1, UserId: meID, CreateAt: 1, FileIds: model.StringArray{f1},
			Metadata: &model.PostMetadata{Files: []*model.FileInfo{{Id: f1, Name: "old.txt", Size: 3}}}},
		p2: {Id: p2, UserId: bobID, CreateAt: 2, FileIds: model.StringArray{f2},
			Metadata: &model.PostMetadata{Files: []*model.FileInfo{{Id: f2, Name: "../../evil.txt", Size: 4}}}},
		fakemm.ID("p3"): {Id: fakemm.ID("p3"), UserId: bobID, CreateAt: 3, Message: "no files"},
	}})
	r := run(app, "file", "list", "town-square")
	mustCode(t, r, 0)
	lines := strings.Split(strings.TrimSpace(r.out), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], f2) || !strings.Contains(lines[1], "old.txt") {
		t.Fatal(r.out)
	}
	s.JSON(http.MethodGet, "/api/v4/posts/"+p2, 200, &model.Post{Id: p2, FileIds: model.StringArray{f2},
		Metadata: &model.PostMetadata{Files: []*model.FileInfo{{Id: f2, Name: "../../evil.txt"}}}})
	s.Handle(http.MethodGet, "/api/v4/files/"+f2, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("evil")) })
	s.JSON(http.MethodGet, "/api/v4/files/"+f1+"/info", 200, &model.FileInfo{Id: f1, Name: "old.txt"})
	s.Handle(http.MethodGet, "/api/v4/files/"+f1, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("old")) })
	dir := t.TempDir()
	mustCode(t, run(app, "file", "download", p2, f1, "-o", dir), 0)
	if b, err := os.ReadFile(filepath.Join(dir, "evil.txt")); err != nil || string(b) != "evil" {
		t.Fatalf("%q %v", b, err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "old.txt")); err != nil || string(b) != "old" {
		t.Fatalf("%q %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(filepath.Dir(dir)), "evil.txt")); err == nil {
		t.Fatal("file written outside the output directory")
	}
	mustCode(t, run(app, "file", "download", f1, "-o", dir), clierr.CodeUsage)
	mustCode(t, run(app, "file", "download", f1, "-o", dir, "--force"), 0)
	mustCode(t, run(app, "file", "download", f1, "-o", filepath.Join(dir, "missing")), clierr.CodeUsage)
}

func TestFileReplaceRemove(t *testing.T) {
	app, s := newTestApp(t)
	p, f1, f2 := fakemm.ID("p"), fakemm.ID("f1"), fakemm.ID("f2")
	s.JSON(http.MethodGet, "/api/v4/posts/"+p, 200, &model.Post{Id: p, ChannelId: townSq, Message: "text", FileIds: model.StringArray{f1, f2}})
	s.JSON(http.MethodPost, "/api/v4/files", 201, &model.FileUploadResponse{FileInfos: []*model.FileInfo{{Id: fakemm.ID("new")}}})
	var patched []string
	s.Handle(http.MethodPut, "/api/v4/posts/"+p+"/patch", func(w http.ResponseWriter, r *http.Request) {
		var patch model.PostPatch
		_ = json.NewDecoder(r.Body).Decode(&patch)
		patched = *patch.FileIds
		fakemm.WriteJSON(w, 200, &model.Post{Id: p})
	})
	file := writeFiles(t, "new.txt")[0]
	mustCode(t, run(app, "file", "replace", p, f2, file), 0)
	if len(patched) != 2 || patched[0] != f1 || patched[1] != fakemm.ID("new") {
		t.Fatalf("%v", patched)
	}
	mustCode(t, run(app, "file", "remove", p, f1, "--yes"), 0)
	if len(patched) != 1 || patched[0] != f2 {
		t.Fatalf("%v", patched)
	}
	mustCode(t, run(app, "file", "remove", p, fakemm.ID("other"), "--yes"), clierr.CodeNotFound)
	mustCode(t, run(app, "file", "remove", p, f1), clierr.CodeUsage)
}

func TestSafeName(t *testing.T) {
	for in, want := range map[string]string{"a.txt": "a.txt", "../../x": "x", `..\..\y`: "y", "..": "fb", "": "fb", "/": "fb", "dir/": "dir"} {
		if got := safeName(in, "fb"); got != want {
			t.Errorf("safeName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBookmarks(t *testing.T) {
	app, s := newTestApp(t)
	serveTownSquare(s)
	bid := fakemm.ID("bm")
	s.Handle(http.MethodPost, "/api/v4/channels/"+townSq+"/bookmarks", func(w http.ResponseWriter, r *http.Request) {
		var b model.ChannelBookmark
		_ = json.NewDecoder(r.Body).Decode(&b)
		if b.Type != model.ChannelBookmarkLink || b.LinkUrl != "https://docs.example.com" || b.DisplayName != "Docs" {
			fakemm.AppError(w, 400, "bad bookmark")
			return
		}
		b.Id = bid
		fakemm.WriteJSON(w, 201, &model.ChannelBookmarkWithFileInfo{ChannelBookmark: &b})
	})
	mustCode(t, run(app, "bookmark", "add", "town-square", "--url", "https://docs.example.com", "--name", "Docs"), 0)
	mustCode(t, run(app, "bookmark", "add", "town-square", "--name", "Docs"), clierr.CodeUsage)
	s.JSON(http.MethodGet, "/api/v4/channels/"+townSq+"/bookmarks", 200, []*model.ChannelBookmarkWithFileInfo{
		{ChannelBookmark: &model.ChannelBookmark{Id: bid, ChannelId: townSq, DisplayName: "Docs", Type: model.ChannelBookmarkLink, LinkUrl: "https://docs.example.com"}},
	})
	r := run(app, "bookmark", "list", "town-square")
	mustCode(t, r, 0)
	if !strings.Contains(r.out, "Docs  https://docs.example.com") {
		t.Fatal(r.out)
	}
	r = run(app, "bookmark", "get", "town-square", bid, "--json")
	var b bookmarkInfo
	if err := json.Unmarshal([]byte(r.out), &b); err != nil || b.ID != bid {
		t.Fatalf("%q %v", r.out, err)
	}
	mustCode(t, run(app, "bookmark", "get", "town-square", fakemm.ID("none")), clierr.CodeNotFound)
	s.JSON(http.MethodPatch, "/api/v4/channels/"+townSq+"/bookmarks/"+bid, 200, &model.UpdateChannelBookmarkResponse{})
	mustCode(t, run(app, "bookmark", "edit", "town-square", bid, "--name", "Docs 2"), 0)
	mustCode(t, run(app, "bookmark", "edit", "town-square", bid), clierr.CodeUsage)
	s.JSON(http.MethodDelete, "/api/v4/channels/"+townSq+"/bookmarks/"+bid, 200, &model.ChannelBookmarkWithFileInfo{})
	mustCode(t, run(app, "bookmark", "delete", "town-square", bid, "--yes"), 0)
	s.Handle(http.MethodGet, "/api/v4/channels/"+townSq+"/bookmarks", func(w http.ResponseWriter, _ *http.Request) {
		fakemm.AppError(w, http.StatusNotImplemented, "not implemented")
	})
	r = run(app, "bookmark", "list", "town-square")
	mustCode(t, r, clierr.CodeNotFound)
	if !strings.Contains(r.err, "not supported") {
		t.Fatal(r.err)
	}
}

// TestChannelListAllTeamsRequestCount checks that listing the channels of all
// teams costs the same few requests whatever the number of teams.
func TestChannelListAllTeamsRequestCount(t *testing.T) {
	app, s := newTestApp(t)
	cfg, err := app.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Profiles["test"].Team = ""
	if err := app.Store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	other := fakemm.ID("otherteam")
	dm, gone := fakemm.ID("dm"), fakemm.ID("gone")
	s.JSON(http.MethodPost, "/api/v4/users/ids", 200, []*model.User{{Id: bobID, Username: "bob"}})
	s.JSON(http.MethodGet, "/api/v4/users/"+meID+"/teams", 200, []*model.Team{
		{Id: teamID, Name: "myteam"}, {Id: other, Name: "otherteam"},
	})
	// Per-team routes, as the server answers them: direct channels come with
	// every team.
	s.JSON(http.MethodGet, "/api/v4/users/"+meID+"/teams/"+teamID+"/channels", 200, []*model.Channel{
		{Id: townSq, TeamId: teamID, Name: "town-square", DisplayName: "Town Square", Type: model.ChannelTypeOpen, TotalMsgCount: 10},
		{Id: dm, Name: meID + "__" + bobID, Type: model.ChannelTypeDirect, TotalMsgCount: 5},
	})
	s.JSON(http.MethodGet, "/api/v4/users/"+meID+"/teams/"+other+"/channels", 200, []*model.Channel{
		{Id: fakemm.ID("notes"), TeamId: other, Name: "notes", DisplayName: "Notes", Type: model.ChannelTypePrivate, TotalMsgCount: 3},
		{Id: dm, Name: meID + "__" + bobID, Type: model.ChannelTypeDirect, TotalMsgCount: 5},
	})
	s.JSON(http.MethodGet, "/api/v4/users/"+meID+"/teams/"+teamID+"/channels/members", 200, []model.ChannelMember{
		{ChannelId: townSq, MsgCount: 8, MentionCount: 1}, {ChannelId: dm, MsgCount: 4},
	})
	s.JSON(http.MethodGet, "/api/v4/users/"+meID+"/teams/"+other+"/channels/members", 200, []model.ChannelMember{
		{ChannelId: fakemm.ID("notes"), MsgCount: 3}, {ChannelId: dm, MsgCount: 4},
	})
	// All teams at once. A channel of a team the user is no longer in is
	// left out, as before.
	s.JSON(http.MethodGet, "/api/v4/users/"+meID+"/channels", 200, []*model.Channel{
		{Id: townSq, TeamId: teamID, Name: "town-square", DisplayName: "Town Square", Type: model.ChannelTypeOpen, TotalMsgCount: 10},
		{Id: fakemm.ID("notes"), TeamId: other, Name: "notes", DisplayName: "Notes", Type: model.ChannelTypePrivate, TotalMsgCount: 3},
		{Id: gone, TeamId: fakemm.ID("left"), Name: "gone", DisplayName: "Gone", Type: model.ChannelTypeOpen, TotalMsgCount: 9},
		{Id: dm, Name: meID + "__" + bobID, Type: model.ChannelTypeDirect, TotalMsgCount: 5},
	})
	s.Handle(http.MethodGet, "/api/v4/users/"+meID+"/channel_members", func(w http.ResponseWriter, r *http.Request) {
		var page []model.ChannelMemberWithTeamData
		if r.URL.Query().Get("page") == "0" {
			for _, m := range []model.ChannelMember{
				{ChannelId: townSq, MsgCount: 8, MentionCount: 1}, {ChannelId: fakemm.ID("notes"), MsgCount: 3},
				{ChannelId: dm, MsgCount: 4}, {ChannelId: gone, MsgCount: 1},
			} {
				page = append(page, model.ChannelMemberWithTeamData{ChannelMember: m})
			}
		}
		fakemm.WriteJSON(w, 200, page)
	})

	r := run(app, "channel", "list")
	mustCode(t, r, 0)
	want := "private  Notes  otherteam/notes\nopen     Town Square  myteam/town-square\ndirect   @bob  " + dm + "\n"
	if r.out != want {
		t.Fatalf("got\n%s\nwant\n%s", r.out, want)
	}
	if n := s.Total(); n > 3 {
		t.Errorf("%d requests for channel list, want at most 3 (teams, channels, user names)", n)
	}

	before := s.Total()
	r = run(app, "channel", "list", "--unread")
	mustCode(t, r, 0)
	want = "open     Town Square  myteam/town-square  unread 2, mentions 1\ndirect   @bob  " + dm + "  unread 1\n"
	if r.out != want {
		t.Fatalf("got\n%s\nwant\n%s", r.out, want)
	}
	if n := s.Total() - before; n > 4 {
		t.Errorf("%d requests for channel list --unread, want at most 4", n)
	}
}
