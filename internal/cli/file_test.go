package cli

import (
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"

	"github.com/VitalyOstanin/mm-cli/internal/clierr"
	"github.com/VitalyOstanin/mm-cli/internal/fakemm"
)

// servePostFiles serves a post with the given attachments and their contents.
func servePostFiles(s *fakemm.Server, post string, infos []*model.FileInfo, contents []string) {
	ids := model.StringArray{}
	for _, f := range infos {
		ids = append(ids, f.Id)
	}
	s.JSON(http.MethodGet, "/api/v4/posts/"+post, 200, &model.Post{Id: post, FileIds: ids, Metadata: &model.PostMetadata{Files: infos}})
	for i, f := range infos {
		body := contents[i]
		s.Handle(http.MethodGet, "/api/v4/files/"+f.Id, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) })
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// dirNames lists the entries of dir, to catch leftover temporary files.
func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestFileDownloadSameNames(t *testing.T) {
	app, s := newTestApp(t)
	p, f1, f2, f3 := fakemm.ID("p"), fakemm.ID("f1"), fakemm.ID("f2"), fakemm.ID("f3")
	servePostFiles(s, p, []*model.FileInfo{{Id: f1, Name: "image.png"}, {Id: f2, Name: "image.png"}, {Id: f3, Name: "IMAGE.png"}},
		[]string{"one", "two", "three"})
	s.JSON(http.MethodGet, "/api/v4/files/"+f1+"/info", 200, &model.FileInfo{Id: f1, Name: "image.png"})
	for _, force := range []bool{false, true} {
		dir := t.TempDir()
		args := []string{"file", "download", p, f1, "-o", dir}
		if force {
			args = append(args, "--force")
		}
		r := run(app, args...)
		mustCode(t, r, 0)
		if got := readFile(t, filepath.Join(dir, "image.png")); got != "one" {
			t.Fatalf("force=%v image.png: %q", force, got)
		}
		if got := readFile(t, filepath.Join(dir, "image (2).png")); got != "two" {
			t.Fatalf("force=%v image (2).png: %q", force, got)
		}
		if got := readFile(t, filepath.Join(dir, "IMAGE (3).png")); got != "three" {
			t.Fatalf("force=%v IMAGE (3).png: %q", force, got)
		}
		// f1 is given twice (inside the post and by id) and is saved once.
		if n := strings.Count(r.out, "Downloaded:"); n != 3 || len(dirNames(t, dir)) != 3 {
			t.Fatalf("force=%v: %d lines, files %v\n%s", force, n, dirNames(t, dir), r.out)
		}
	}
}

func TestFileDownloadChecksConflictsFirst(t *testing.T) {
	app, s := newTestApp(t)
	p, f1, f2 := fakemm.ID("p"), fakemm.ID("f1"), fakemm.ID("f2")
	servePostFiles(s, p, []*model.FileInfo{{Id: f1, Name: "a.txt"}, {Id: f2, Name: "b.txt"}}, []string{"new a", "new b"})
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("old b"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := run(app, "file", "download", p, "-o", dir)
	mustCode(t, r, clierr.CodeUsage)
	if s.Count(http.MethodGet, "/api/v4/files/"+f1) != 0 || s.Count(http.MethodGet, "/api/v4/files/"+f2) != 0 {
		t.Fatal("files were downloaded before the conflict was reported")
	}
	if _, err := os.Stat(filepath.Join(dir, "a.txt")); err == nil {
		t.Fatal("a.txt written although b.txt conflicts")
	}
	if !strings.Contains(r.err, "b.txt") {
		t.Fatal(r.err)
	}
	mustCode(t, run(app, "file", "download", p, "-o", dir, "--force"), 0)
	if readFile(t, filepath.Join(dir, "b.txt")) != "new b" {
		t.Fatal("b.txt not overwritten with --force")
	}
}

func TestFileDownloadForceReplacesSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic links need extra privileges on Windows")
	}
	app, s := newTestApp(t)
	p, f1 := fakemm.ID("p"), fakemm.ID("f1")
	servePostFiles(s, p, []*model.FileInfo{{Id: f1, Name: "report.txt"}}, []string{"attachment"})
	root := t.TempDir()
	dir := filepath.Join(root, "out")
	victim := filepath.Join(root, "victim")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(victim, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(dir, "report.txt")); err != nil {
		t.Fatal(err)
	}
	mustCode(t, run(app, "file", "download", p, "-o", dir, "--force"), 0)
	if got := readFile(t, victim); got != "keep me" {
		t.Fatalf("symlink target overwritten: %q", got)
	}
	st, err := os.Lstat(filepath.Join(dir, "report.txt"))
	if err != nil || st.Mode()&os.ModeSymlink != 0 || readFile(t, filepath.Join(dir, "report.txt")) != "attachment" {
		t.Fatalf("report.txt is not the downloaded file: %v %v", st, err)
	}
}

func TestFileDownloadLeavesNoPartialFile(t *testing.T) {
	app, s := newTestApp(t)
	p, f1, f2 := fakemm.ID("p"), fakemm.ID("f1"), fakemm.ID("f2")
	servePostFiles(s, p, []*model.FileInfo{{Id: f1, Name: "ok.txt"}, {Id: f2, Name: "big.bin"}}, []string{"ok", ""})
	// The body is cut short: the connection closes before Content-Length bytes.
	s.Handle(http.MethodGet, "/api/v4/files/"+f2, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "1000")
		_, _ = w.Write([]byte("partial"))
	})
	dir := t.TempDir()
	r := run(app, "file", "download", p, "-o", dir)
	if r.code == 0 {
		t.Fatal("a truncated download succeeded")
	}
	if names := dirNames(t, dir); len(names) != 1 || names[0] != "ok.txt" {
		t.Fatalf("leftover files: %v", names)
	}
}

func TestSafeNameWindows(t *testing.T) {
	for _, c := range []struct{ in, win, unix string }{
		{"report.txt", "report.txt", "report.txt"},
		{"C:evil", "C_evil", "C:evil"},
		{"CON", "_CON", "CON"},
		{"nul.txt", "_nul.txt", "nul.txt"},
		{"Com1.log", "_Com1.log", "Com1.log"},
		{"lpt9", "_lpt9", "lpt9"},
		{"console.txt", "console.txt", "console.txt"},
		{`a<b>"c|d?e*.txt`, "a_b__c_d_e_.txt", `a<b>"c|d?e*.txt`},
		{"name. ", "name", "name. "},
		{"...", "fallback", "..."},
		{"dir\\x.txt", "x.txt", "x.txt"},
		{"../../evil.txt", "evil.txt", "evil.txt"},
		{"..", "fallback", "fallback"},
		{"", "fallback", "fallback"},
	} {
		if got := sanitizeName(c.in, "fallback", true); got != c.win {
			t.Errorf("windows %q: got %q, want %q", c.in, got, c.win)
		}
		if got := sanitizeName(c.in, "fallback", false); got != c.unix {
			t.Errorf("unix %q: got %q, want %q", c.in, got, c.unix)
		}
	}
}

func TestUniqueName(t *testing.T) {
	taken := map[string]bool{}
	for _, c := range []struct{ in, want string }{
		{"image.png", "image.png"},
		{"image.png", "image (2).png"},
		{"Image.PNG", "Image (3).PNG"},
		{"image (2).png", "image (2) (2).png"},
		{"README", "README"},
		{"readme", "readme (2)"},
		{".bashrc", ".bashrc"},
		{".bashrc", ".bashrc (2)"},
		{"archive.tar.gz", "archive.tar.gz"},
		{"archive.tar.gz", "archive.tar (2).gz"},
	} {
		if got := uniqueName(c.in, taken); got != c.want {
			t.Errorf("%q: got %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFileDownloadFileFlagSkipsPostProbe(t *testing.T) {
	app, s := newTestApp(t)
	f := fakemm.ID("f")
	s.JSON(http.MethodGet, "/api/v4/files/"+f+"/info", 200, &model.FileInfo{Id: f, Name: "a.txt"})
	s.Handle(http.MethodGet, "/api/v4/files/"+f, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("x")) })
	base := s.Total()
	mustCode(t, run(app, "file", "download", f, "--file", "-o", t.TempDir()), 0)
	if n := s.Count(http.MethodGet, "/api/v4/posts/"+f); n != 0 {
		t.Fatalf("--file still asked for a post: %d requests", n)
	}
	if n := s.Total() - base; n != 2 {
		t.Fatalf("--file: %d requests, want 2 (info and download)", n)
	}
}

func TestFileDownloadPostFlagSkipsFileProbe(t *testing.T) {
	app, s := newTestApp(t)
	f := fakemm.ID("f")
	s.JSON(http.MethodGet, "/api/v4/files/"+f+"/info", 200, &model.FileInfo{Id: f, Name: "a.txt"})
	r := run(app, "file", "download", f, "--post", "-o", t.TempDir())
	mustCode(t, r, clierr.CodeNotFound)
	if n := s.Count(http.MethodGet, "/api/v4/files/"+f+"/info"); n != 0 {
		t.Fatalf("--post still tried the id as a file: %d requests", n)
	}
	if !strings.Contains(r.err, "message") {
		t.Fatal(r.err)
	}
}

func TestFileDownloadFileAndPostConflict(t *testing.T) {
	app, s := newTestApp(t)
	base := s.Total()
	r := run(app, "file", "download", fakemm.ID("f"), "--file", "--post", "-o", t.TempDir())
	mustCode(t, r, clierr.CodeUsage)
	if !strings.Contains(r.err, "cannot be used together") {
		t.Fatal(r.err)
	}
	if n := s.Total() - base; n != 0 {
		t.Fatalf("%d requests sent despite conflicting flags", n)
	}
}
