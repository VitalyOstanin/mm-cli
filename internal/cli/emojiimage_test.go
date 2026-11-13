package cli

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"

	"github.com/VitalyOstanin/mm-cli/internal/fakemm"
)

// pngBytes is the PNG signature plus filler: enough for content sniffing.
var pngBytes = append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 16)...)

// useCacheDir points the emoji cache at dir on every system.
func useCacheDir(t *testing.T, dir string) {
	t.Helper()
	old := userCacheDir
	userCacheDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { userCacheDir = old })
}

// collectNotifications makes watch --notify record what it would show.
func collectNotifications(app *App) (get func() []Notification) {
	var mu sync.Mutex
	var got []Notification
	app.Notify = func(_ context.Context, n Notification) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, n)
	}
	return func() []Notification {
		mu.Lock()
		defer mu.Unlock()
		return append([]Notification(nil), got...)
	}
}

func TestWatchNotifyCustomEmojiImageFetchedOnce(t *testing.T) {
	cache := t.TempDir()
	useCacheDir(t, cache)
	r := reactServer(t, func(town string) [][]byte {
		return [][]byte{
			reactionEvent(t, town, fakemm.ID("mine"), bobID, "partyparrot"),
			reactionEvent(t, town, fakemm.ID("mine"), fakemm.ID("dave"), "partyparrot"),
		}
	})
	r.s.JSON(http.MethodPost, "/api/v4/users/ids", http.StatusOK,
		[]*model.User{{Id: bobID, Username: "bob"}, {Id: fakemm.ID("dave"), Username: "dave"}})
	r.s.JSON(http.MethodGet, "/api/v4/emoji/name/partyparrot", http.StatusOK, &model.Emoji{Id: fakemm.ID("emoji"), Name: "partyparrot"})
	r.s.Handle(http.MethodGet, "/api/v4/emoji/"+fakemm.ID("emoji")+"/image", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(pngBytes)
	})
	get := collectNotifications(r.app)
	out, _, stop := startWatch(t, r.app, "--reactions", "--notify")
	waitFor(t, "both reactions", func() bool { return strings.Count(out.String(), "mattermost://") >= 2 })
	stop()
	got := get()
	if len(got) != 2 {
		t.Fatalf("%d notifications, want 2: %+v", len(got), got)
	}
	for _, n := range got {
		if !strings.HasSuffix(n.Title, "reacted :partyparrot:") {
			t.Errorf("title %q", n.Title)
		}
		if n.Image == "" || !strings.HasPrefix(n.Image, cache) || filepath.Ext(n.Image) != ".png" {
			t.Errorf("image %q is not a cached png under %s", n.Image, cache)
		}
		if n.Image != got[0].Image {
			t.Errorf("images differ: %q and %q", n.Image, got[0].Image)
		}
	}
	if b, err := os.ReadFile(got[0].Image); err != nil || string(b) != string(pngBytes) {
		t.Errorf("cached file: %v", err)
	}
	if runtime.GOOS != "windows" {
		if fi, err := os.Stat(got[0].Image); err != nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("file mode %v, %v", fi.Mode().Perm(), err)
		}
		if fi, err := os.Stat(filepath.Dir(got[0].Image)); err != nil || fi.Mode().Perm() != 0o700 {
			t.Errorf("dir mode %v, %v", fi.Mode().Perm(), err)
		}
	}
	if n := r.s.Count(http.MethodGet, "/api/v4/emoji/name/partyparrot"); n != 1 {
		t.Errorf("%d lookups, want 1", n)
	}
	if n := r.s.Count(http.MethodGet, "/api/v4/emoji/"+fakemm.ID("emoji")+"/image"); n != 1 {
		t.Errorf("%d downloads, want 1", n)
	}
}

func TestWatchNotifyFailedEmojiImageStillNotifies(t *testing.T) {
	useCacheDir(t, t.TempDir())
	r := reactServer(t, func(town string) [][]byte {
		return [][]byte{
			reactionEvent(t, town, fakemm.ID("mine"), bobID, "partyparrot"),
			reactionEvent(t, town, fakemm.ID("mine"), bobID, "partyparrot"),
		}
	})
	r.s.JSON(http.MethodGet, "/api/v4/emoji/name/partyparrot", http.StatusOK, &model.Emoji{Id: fakemm.ID("emoji"), Name: "partyparrot"})
	r.s.Handle(http.MethodGet, "/api/v4/emoji/"+fakemm.ID("emoji")+"/image", func(w http.ResponseWriter, _ *http.Request) {
		fakemm.AppError(w, http.StatusInternalServerError, "boom")
	})
	get := collectNotifications(r.app)
	out, _, stop := startWatch(t, r.app, "--reactions", "--notify")
	waitFor(t, "the reaction", func() bool { return strings.Contains(out.String(), "mattermost://") })
	waitFor(t, "the notification", func() bool { return len(get()) >= 1 })
	if code := stop(); code != 0 {
		t.Fatalf("exit code %d", code)
	}
	got := get()
	if got[0].Image != "" || !strings.HasSuffix(got[0].Title, "reacted :partyparrot:") {
		t.Errorf("notification %+v", got[0])
	}
}

func TestNotifyImageReachesEachNotifier(t *testing.T) {
	n := Notification{Title: "t", Body: "b", AppLink: "mattermost://h/t/pl/p", Image: "/cache/mm-cli/emoji/x/e.png"}
	c := notifyCommand("darwin", true, n, "g")
	if !strings.Contains(strings.Join(c.args, "|"), "-contentImage|/cache/mm-cli/emoji/x/e.png") {
		t.Errorf("terminal-notifier args %v", c.args)
	}
	if sc := toastScript(n); !strings.Contains(sc, "<image placement=") {
		t.Errorf("toast has no image element")
	}
	if sc := toastScript(Notification{Title: "t", Body: "b"}); strings.Contains(sc, "<image") {
		t.Errorf("toast without image has an image element")
	}
	if got := toastImageURL(`C:\Users\alice\c.png`); got != "file:///C:/Users/alice/c.png" {
		t.Errorf("toast image url %q", got)
	}
	d, bus, stop := newBusNotifier()
	d.Notify(context.Background(), n)
	stop()
	if len(bus.shown) != 1 || bus.shown[0].image != n.Image {
		t.Errorf("bus got %+v", bus.shown)
	}
}

// emojiFetch runs one emoji image download against a fake server and returns
// the resulting path and the cache directory.
func emojiFetch(t *testing.T, body []byte) (string, string) {
	t.Helper()
	cache := t.TempDir()
	useCacheDir(t, cache)
	app, s := newTestApp(t)
	s.JSON(http.MethodGet, "/api/v4/emoji/name/parrot", http.StatusOK, &model.Emoji{Id: fakemm.ID("emoji"), Name: "parrot"})
	s.Handle(http.MethodGet, "/api/v4/emoji/"+fakemm.ID("emoji")+"/image", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	})
	sess, err := app.openSession()
	if err != nil {
		t.Fatal(err)
	}
	return newEmojiImages(sess).path(context.Background(), "parrot"), cache
}

func countFiles(t *testing.T, root string) int {
	t.Helper()
	n := 0
	_ = filepath.WalkDir(root, func(_ string, d os.DirEntry, _ error) error {
		if d != nil && !d.IsDir() {
			n++
		}
		return nil
	})
	return n
}

func TestEmojiImageOversizedNotSaved(t *testing.T) {
	big := append(append([]byte(nil), pngBytes...), make([]byte, maxEmojiImageSize)...)
	p, cache := emojiFetch(t, big)
	if p != "" || countFiles(t, cache) != 0 {
		t.Errorf("path %q, %d files; want none", p, countFiles(t, cache))
	}
}

func TestEmojiImageNonImageNotSaved(t *testing.T) {
	p, cache := emojiFetch(t, []byte("<html>not an image</html>"))
	if p != "" || countFiles(t, cache) != 0 {
		t.Errorf("path %q, %d files; want none", p, countFiles(t, cache))
	}
}

func TestWriteFileAtomicLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	if err := writeFileAtomic(filepath.Join(dir, "a.png"), pngBytes); err != nil {
		t.Fatal(err)
	}
	if n := countFiles(t, dir); n != 1 {
		t.Errorf("%d files, want 1", n)
	}
	// A rename onto a directory fails; the temporary file must be removed.
	target := filepath.Join(dir, "d")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "x"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(target, pngBytes); err == nil {
		t.Fatal("want error")
	}
	if n := countFiles(t, dir); n != 2 {
		t.Errorf("%d files, want 2 (temp file left over)", n)
	}
}
