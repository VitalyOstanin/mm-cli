package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"

	"github.com/VitalyOstanin/mm-cli/internal/clierr"
	"github.com/VitalyOstanin/mm-cli/internal/fakemm"
)

// mustJSON fails unless stdout is one JSON value, and decodes it into v.
func mustJSON(t *testing.T, r result, v any) {
	t.Helper()
	mustCode(t, r, 0)
	if err := json.Unmarshal([]byte(r.out), v); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, r.out)
	}
}

func TestChangingCommandsPrintJSON(t *testing.T) {
	app, s := newTestApp(t)
	serveTownSquare(s)
	p, f1, f2, bid := fakemm.ID("p"), fakemm.ID("f1"), fakemm.ID("f2"), fakemm.ID("bm")
	s.JSON(http.MethodPost, "/api/v4/posts/"+p+"/pin", 200, map[string]string{"status": "OK"})
	s.JSON(http.MethodPost, "/api/v4/posts/"+p+"/unpin", 200, map[string]string{"status": "OK"})
	s.JSON(http.MethodPut, "/api/v4/users/"+meID+"/preferences", 200, map[string]string{"status": "OK"})
	s.JSON(http.MethodPost, "/api/v4/users/"+meID+"/preferences/delete", 200, map[string]string{"status": "OK"})
	s.JSON(http.MethodDelete, "/api/v4/channels/"+townSq+"/bookmarks/"+bid, 200, &model.ChannelBookmarkWithFileInfo{})
	s.JSON(http.MethodDelete, "/api/v4/channels/"+townSq, 200, map[string]string{"status": "OK"})
	s.JSON(http.MethodPost, "/api/v4/channels/"+townSq+"/restore", 200, &model.Channel{Id: townSq})
	s.JSON(http.MethodPost, "/api/v4/channels/members/"+meID+"/view", 200, &model.ChannelViewResponse{Status: "OK"})
	s.JSON(http.MethodDelete, "/api/v4/posts/"+p, 200, map[string]string{"status": "OK"})

	for _, tc := range []struct {
		args []string
		want actionResult
	}{
		{[]string{"pin", "add", p}, actionResult{Action: "pinned", ID: p}},
		{[]string{"pin", "remove", p}, actionResult{Action: "unpinned", ID: p}},
		{[]string{"saved", "add", p}, actionResult{Action: "saved", ID: p}},
		{[]string{"saved", "remove", p}, actionResult{Action: "unsaved", ID: p}},
		{[]string{"bookmark", "delete", "town-square", bid, "--yes"}, actionResult{Action: "deleted", ID: bid, ChannelID: townSq}},
		{[]string{"channel", "archive", "town-square"}, actionResult{Action: "archived", ID: townSq}},
		{[]string{"channel", "unarchive", "town-square"}, actionResult{Action: "unarchived", ID: townSq}},
		{[]string{"channel", "read", "town-square"}, actionResult{Action: "marked_read", ID: townSq}},
	} {
		var got actionResult
		mustJSON(t, run(app, append(tc.args, "--json")...), &got)
		if got != tc.want {
			t.Errorf("%v: got %+v, want %+v", tc.args, got, tc.want)
		}
	}

	var deleted []actionResult
	mustJSON(t, run(app, "post", "delete", p, "--yes", "--json"), &deleted)
	if len(deleted) != 1 || deleted[0] != (actionResult{Action: "deleted", ID: p}) {
		t.Fatalf("%+v", deleted)
	}

	s.JSON(http.MethodGet, "/api/v4/posts/"+p, 200, &model.Post{Id: p, ChannelId: townSq, Message: "text", FileIds: model.StringArray{f1, f2}})
	s.JSON(http.MethodPost, "/api/v4/files", 201, &model.FileUploadResponse{FileInfos: []*model.FileInfo{{Id: fakemm.ID("new")}}})
	s.JSON(http.MethodPut, "/api/v4/posts/"+p+"/patch", 200, &model.Post{Id: p})
	var replaced actionResult
	mustJSON(t, run(app, "file", "replace", p, f2, writeFiles(t, "new.txt")[0], "--json"), &replaced)
	if replaced != (actionResult{Action: "replaced", PostID: p, OldFileID: f2, FileID: fakemm.ID("new")}) {
		t.Fatalf("%+v", replaced)
	}
	var removed actionResult
	mustJSON(t, run(app, "file", "remove", p, f1, "--yes", "--json"), &removed)
	if removed != (actionResult{Action: "removed", PostID: p, FileID: f1}) {
		t.Fatalf("%+v", removed)
	}
}

func TestPostDeleteJSONReportsDoneBeforeError(t *testing.T) {
	app, s := newTestApp(t)
	id, id2 := fakemm.ID("p1"), fakemm.ID("p2")
	s.JSON(http.MethodDelete, "/api/v4/posts/"+id, 200, map[string]string{"status": "OK"})
	s.Handle(http.MethodDelete, "/api/v4/posts/"+id2, func(w http.ResponseWriter, _ *http.Request) {
		fakemm.AppError(w, http.StatusForbidden, "not your post")
	})
	r := run(app, "post", "delete", id, id2, "--yes", "--json")
	mustCode(t, r, clierr.CodeForbidden)
	var deleted []actionResult
	if err := json.Unmarshal([]byte(r.out), &deleted); err != nil || len(deleted) != 1 || deleted[0].ID != id {
		t.Fatalf("%q %v", r.out, err)
	}
}

func TestFileDownloadJSON(t *testing.T) {
	app, s := newTestApp(t)
	p, f1, f2 := fakemm.ID("p"), fakemm.ID("f1"), fakemm.ID("f2")
	servePostFiles(s, p, []*model.FileInfo{{Id: f1, Name: "a.txt"}, {Id: f2, Name: "b.txt"}}, []string{"aa", "bbb"})
	dir := t.TempDir()
	var got []downloadedFile
	mustJSON(t, run(app, "file", "download", p, "-o", dir, "--json"), &got)
	want := []downloadedFile{
		{FileID: f1, Path: filepath.Join(dir, "a.txt"), Size: 2},
		{FileID: f2, Path: filepath.Join(dir, "b.txt"), Size: 3},
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestUnknownCommandJSONError(t *testing.T) {
	for _, args := range [][]string{{"--json", "bogus"}, {"bogus", "--json"}, {"--json=true", "bogus"}} {
		app := newEmptyApp(t)
		r := run(app, args...)
		mustCode(t, r, clierr.CodeUsage)
		var e struct {
			Error struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(r.err), &e); err != nil || e.Error.Code != clierr.CodeUsage {
			t.Fatalf("%v: stderr %q %v", args, r.err, err)
		}
	}
	r := run(newEmptyApp(t), "bogus", "--", "--json")
	if json.Valid([]byte(r.err)) {
		t.Fatalf("--json after -- is not a flag: %q", r.err)
	}
}

func TestSignalExitCodes(t *testing.T) {
	for _, tc := range []struct {
		cause error
		code  int
	}{
		{clierr.Interrupted(), clierr.CodeInterrupted},
		{clierr.Terminated(), clierr.CodeTerminated},
	} {
		app, s := newTestApp(t)
		serveTownSquare(s)
		ctx, cancel := context.WithCancelCause(context.Background())
		cancel(tc.cause)
		r := result{}
		out, errb := &syncBuffer{}, &syncBuffer{}
		app.Out, app.Err = out, errb
		r.code = Execute(ctx, app, []string{"channel", "read", "town-square"})
		r.out, r.err = out.String(), errb.String()
		mustCode(t, r, tc.code)

		// watch stops quietly on cancellation, but the signal still sets the code.
		app.Out, app.Err = out, errb
		if code := Execute(ctx, app, []string{"watch"}); code != tc.code {
			t.Fatalf("watch: exit code %d, want %d; stderr %s", code, tc.code, errb.String())
		}
	}
}
