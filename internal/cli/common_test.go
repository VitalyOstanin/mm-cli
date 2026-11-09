package cli

import (
	"net/http"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"

	"github.com/VitalyOstanin/mm-cli/internal/clierr"
	"github.com/VitalyOstanin/mm-cli/internal/fakemm"
)

func TestLimitIsCheckedTheSameWayEverywhere(t *testing.T) {
	app, _ := newTestApp(t)
	for _, args := range [][]string{
		{"post", "list", "town-square"},
		{"post", "search", "deploy"},
		{"saved", "list"},
		{"file", "list", "town-square"},
	} {
		for _, n := range []string{"0", "201"} {
			r := run(app, append(args, "-n", n)...)
			mustCode(t, r, clierr.CodeUsage)
			if !strings.Contains(r.err, "-n must be between 1 and 200") {
				t.Errorf("%v -n %s: stderr %q", args, n, r.err)
			}
		}
	}
}

func TestLimitAndTruncateHelp(t *testing.T) {
	app, _ := newTestApp(t)
	for _, tc := range []struct {
		args []string
		want []string
	}{
		{[]string{"post", "list"}, []string{"number of messages (1-200)", "shorten messages to this many characters"}},
		{[]string{"post", "search"}, []string{"number of results (1-200)", "shorten messages to this many characters"}},
		{[]string{"saved", "list"}, []string{"number of messages (1-200)", "shorten messages to this many characters"}},
		{[]string{"file", "list"}, []string{"number of files (1-200)"}},
		{[]string{"pin", "list"}, []string{"shorten messages to this many characters"}},
		{[]string{"post", "thread"}, []string{"shorten messages to this many characters"}},
	} {
		r := run(app, append(tc.args, "--help")...)
		mustCode(t, r, 0)
		for _, w := range tc.want {
			if !strings.Contains(r.out, w) {
				t.Errorf("%v --help lacks %q:\n%s", tc.args, w, r.out)
			}
		}
	}
}

func TestToggleCommandsText(t *testing.T) {
	app, s := newTestApp(t)
	serveTownSquare(s)
	p := fakemm.ID("p")
	s.JSON(http.MethodPost, "/api/v4/posts/"+p+"/pin", 200, map[string]string{"status": "OK"})
	s.JSON(http.MethodPost, "/api/v4/posts/"+p+"/unpin", 200, map[string]string{"status": "OK"})
	s.JSON(http.MethodPut, "/api/v4/users/"+meID+"/preferences", 200, map[string]string{"status": "OK"})
	s.JSON(http.MethodPost, "/api/v4/users/"+meID+"/preferences/delete", 200, map[string]string{"status": "OK"})
	s.JSON(http.MethodDelete, "/api/v4/channels/"+townSq, 200, map[string]string{"status": "OK"})
	s.JSON(http.MethodPost, "/api/v4/channels/"+townSq+"/restore", 200, &model.Channel{Id: townSq})

	for _, tc := range []struct {
		args        []string
		short, want string
	}{
		{[]string{"pin", "add", p}, "Pin a message to its channel", "Pinned: " + p},
		{[]string{"pin", "remove", p}, "Unpin a message", "Unpinned: " + p},
		{[]string{"saved", "add", p}, "Save a message", "Saved: " + p},
		{[]string{"saved", "remove", p}, "Remove a message from saved", "Removed from saved: " + p},
		{[]string{"channel", "archive", "town-square"}, "Archive a channel (reversible with unarchive)", "Archived: " + townSq},
		{[]string{"channel", "unarchive", "town-square"}, "Restore an archived channel", "Unarchived: " + townSq},
	} {
		r := run(app, tc.args...)
		mustCode(t, r, 0)
		if strings.TrimSpace(r.out) != tc.want {
			t.Errorf("%v: stdout %q, want %q", tc.args, r.out, tc.want)
		}
		h := run(app, tc.args[0], tc.args[1], "--help")
		if !strings.HasPrefix(h.out, tc.short) {
			t.Errorf("%v --help starts with %q, want %q", tc.args[:2], h.out, tc.short)
		}
	}
}

func TestUploadWithUnexpectedFileRecords(t *testing.T) {
	app, s := newTestApp(t)
	serveTownSquare(s)
	p, f1 := fakemm.ID("p"), fakemm.ID("f1")
	s.JSON(http.MethodGet, "/api/v4/posts/"+p, 200, &model.Post{Id: p, ChannelId: townSq, FileIds: model.StringArray{f1}})
	s.JSON(http.MethodPost, "/api/v4/files", 201, &model.FileUploadResponse{})
	f := writeFiles(t, "a.txt")[0]

	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"send", "town-square", "-f", f}, "upload " + f + ": the server returned 0 file records"},
		{[]string{"bookmark", "add", "town-square", "-f", f, "--name", "a"}, "upload: the server returned 0 file records"},
		{[]string{"file", "replace", p, f1, f}, "upload: the server returned 0 file records"},
	} {
		r := run(app, tc.args...)
		if r.code == 0 || !strings.Contains(r.err, tc.want) {
			t.Errorf("%v: code %d, stderr %q, want %q", tc.args, r.code, r.err, tc.want)
		}
	}
}
