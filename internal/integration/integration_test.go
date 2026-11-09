//go:build integration

// Package integration runs the mm-cli commands against a real Mattermost
// server. Start one with scripts/integration.sh, or point MM_TEST_URL at a
// throwaway server: the tests create users, a team and content on it.
package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"

	"github.com/VitalyOstanin/mm-cli/internal/cli"
	"github.com/VitalyOstanin/mm-cli/internal/config"
)

const password = "Integration-test-1"

type env struct {
	url        string
	admin, bob *model.Client4
	// alice is the user mm-cli is signed in as.
	alice *model.Client4
	team  *model.Team
	app   *cli.App
	dir   string
}

type result struct {
	code     int
	out, err string
}

// syncBuffer lets a test read the output of a command that is still running.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func (e *env) run(t *testing.T, stdin string, args ...string) result {
	t.Helper()
	out, errb := &bytes.Buffer{}, &bytes.Buffer{}
	e.app.In, e.app.Out, e.app.Err = strings.NewReader(stdin), out, errb
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	code := cli.Execute(ctx, e.app, args)
	return result{code, out.String(), errb.String()}
}

func (e *env) ok(t *testing.T, args ...string) string {
	t.Helper()
	r := e.run(t, "", args...)
	if r.code != 0 {
		t.Fatalf("mm-cli %s: exit %d\n%s%s", strings.Join(args, " "), r.code, r.out, r.err)
	}
	return r.out
}

func (e *env) json(t *testing.T, v any, args ...string) {
	t.Helper()
	out := e.ok(t, append([]string{"--json"}, args...)...)
	if err := json.Unmarshal([]byte(out), v); err != nil {
		t.Fatalf("mm-cli %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func unique(prefix string) string {
	return fmt.Sprintf("%s%d", prefix, time.Now().UnixNano()%1_000_000_000)
}

func mustUser(t *testing.T, c *model.Client4, name string) *model.User {
	t.Helper()
	ctx := context.Background()
	u, _, err := c.CreateUser(ctx, &model.User{Username: name, Email: name + "@example.com", Password: password})
	if err != nil {
		t.Fatalf("create user %s: %v", name, err)
	}
	return u
}

func setup(t *testing.T) *env {
	t.Helper()
	url := os.Getenv("MM_TEST_URL")
	if url == "" {
		t.Skip("MM_TEST_URL is not set; run scripts/integration.sh")
	}
	ctx := context.Background()
	admin := model.NewAPIv4Client(url)
	// The first user of a fresh server is a system admin; later runs reuse it.
	if _, _, err := admin.Login(ctx, "admin", password); err != nil {
		mustUser(t, admin, "admin")
		if _, _, err := admin.Login(ctx, "admin", password); err != nil {
			t.Fatalf("admin login: %v", err)
		}
	}
	aliceName, bobName := unique("alice"), unique("bob")
	alice := model.NewAPIv4Client(url)
	mustUser(t, admin, aliceName)
	bobUser := mustUser(t, admin, bobName)
	if _, _, err := alice.Login(ctx, aliceName, password); err != nil {
		t.Fatalf("alice login: %v", err)
	}
	bob := model.NewAPIv4Client(url)
	if _, _, err := bob.Login(ctx, bobName, password); err != nil {
		t.Fatalf("bob login: %v", err)
	}
	team, _, err := alice.CreateTeam(ctx, &model.Team{Name: unique("myteam"), DisplayName: "My team", Type: model.TeamOpen})
	if err != nil {
		t.Fatalf("create team: %v", err)
	}
	if _, _, err := alice.AddTeamMember(ctx, team.Id, bobUser.Id); err != nil {
		t.Fatalf("add bob: %v", err)
	}

	dir := t.TempDir()
	e := &env{url: url, admin: admin, bob: bob, alice: alice, team: team, dir: dir, app: &cli.App{
		Store:       config.Store{ConfigDir: filepath.Join(dir, "config"), CacheDir: filepath.Join(dir, "cache")},
		Env:         func(string) string { return "" },
		Now:         time.Now,
		Location:    time.UTC,
		OpenBrowser: func(string) error { t.Error("unexpected browser launch"); return nil },
		IsTerminal:  func() bool { return false },
		Version:     "integration",
		DefaultRate: config.RateLimit{PerSecond: 20, Burst: 20},
	}}
	r := e.run(t, alice.AuthToken+"\n", "auth", "login", url, "--token-stdin", "--profile", "it", "--team", team.Name)
	if r.code != 0 {
		t.Fatalf("auth login: exit %d\n%s%s", r.code, r.out, r.err)
	}
	return e
}

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

type post struct {
	ID         string `json:"id"`
	ChannelID  string `json:"channel_id"`
	RootID     string `json:"root_id"`
	Message    string `json:"message"`
	ReplyCount int64  `json:"reply_count"`
	Edited     bool   `json:"edited"`
	Pinned     bool   `json:"pinned"`
	Files      []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"files"`
}

func TestCommands(t *testing.T) {
	e := setup(t)
	ch := "town-square"

	e.ok(t, "auth", "check")
	e.ok(t, "profile", "list")

	// Posts.
	a := writeFile(t, e.dir, "a.txt", "first file\n")
	b := writeFile(t, e.dir, "b.md", "# second file\n")
	var sent struct{ ID string }
	e.json(t, &sent, "send", ch, "hello from the integration test", "-f", a, "-f", b)
	var got post
	e.json(t, &got, "post", "get", sent.ID)
	if got.Message != "hello from the integration test" || len(got.Files) != 2 {
		t.Fatalf("post get: %+v", got)
	}
	var list []post
	e.json(t, &list, "messages", ch, "-n", "5")
	if len(list) == 0 || list[len(list)-1].ID != sent.ID {
		t.Fatalf("messages: the new post is not last: %+v", list)
	}

	e.ok(t, "post", "edit", sent.ID, "edited text")
	e.json(t, &got, "post", "get", sent.ID)
	if got.Message != "edited text" || !got.Edited {
		t.Fatalf("after edit: %+v", got)
	}

	// Edit history and restore (open question 2 of the spec).
	var versions []post
	e.json(t, &versions, "post", "history", sent.ID)
	if len(versions) == 0 {
		t.Fatal("post history is empty after an edit")
	}
	if r := e.run(t, "", "post", "restore", sent.ID, versions[0].ID); r.code != 0 {
		t.Logf("post restore is not available: exit %d %s", r.code, r.err)
	} else {
		e.json(t, &got, "post", "get", sent.ID)
		t.Logf("post restore: message is now %q", got.Message)
	}

	// Threads.
	var reply struct{ ID string }
	e.json(t, &reply, "send", ch, "a reply", "--reply", sent.ID)
	var thread []post
	e.json(t, &thread, "post", "thread", sent.ID)
	if len(thread) != 2 || thread[1].RootID != sent.ID {
		t.Fatalf("thread: %+v", thread)
	}
	// The id of a reply shows the same whole thread.
	var fromReply []post
	e.json(t, &fromReply, "post", "thread", reply.ID)
	if len(fromReply) != 2 || fromReply[0].ID != sent.ID {
		t.Fatalf("thread from a reply id: %+v", fromReply)
	}

	// Files (open questions 1 and 3).
	var files []struct {
		ID     string `json:"id"`
		PostID string `json:"post_id"`
	}
	e.json(t, &files, "file", "list", ch)
	if len(files) < 2 {
		t.Fatalf("file list: %+v", files)
	}
	dl := filepath.Join(e.dir, "dl")
	if err := os.Mkdir(dl, 0o700); err != nil {
		t.Fatal(err)
	}
	e.ok(t, "file", "download", sent.ID, "-o", dl)
	if data, err := os.ReadFile(filepath.Join(dl, "a.txt")); err != nil || string(data) != "first file\n" {
		t.Fatalf("downloaded a.txt: %q %v", data, err)
	}
	if r := e.run(t, "", "file", "download", sent.ID, "-o", dl); r.code != 2 {
		t.Fatalf("second download without --force: exit %d", r.code)
	}
	e.json(t, &got, "post", "get", sent.ID)
	c := writeFile(t, e.dir, "c.txt", "replacement\n")
	if r := e.run(t, "", "file", "replace", sent.ID, got.Files[0].ID, c); r.code != 0 {
		t.Logf("file replace is not available: exit %d %s", r.code, r.err)
	} else {
		e.ok(t, "file", "remove", sent.ID, got.Files[1].ID, "--yes")
		e.json(t, &got, "post", "get", sent.ID)
		if len(got.Files) != 1 || got.Files[0].Name != "c.txt" {
			t.Fatalf("after replace and remove: %+v", got.Files)
		}
	}

	// Pins and saved messages.
	e.ok(t, "pin", "add", sent.ID)
	var pins []post
	e.json(t, &pins, "pin", "list", ch)
	if len(pins) != 1 || pins[0].ID != sent.ID {
		t.Fatalf("pin list: %+v", pins)
	}
	e.ok(t, "pin", "remove", sent.ID)
	e.ok(t, "saved", "add", sent.ID)
	var saved []post
	e.json(t, &saved, "saved", "list")
	if len(saved) != 1 || saved[0].ID != sent.ID {
		t.Fatalf("saved list: %+v", saved)
	}
	e.ok(t, "saved", "remove", sent.ID)

	// Search goes through the server index, which is built asynchronously:
	// retry until the post shows up or searchIndexTimeout expires.
	var found []post
	searchDeadline := time.Now().Add(searchIndexTimeout)
	for {
		e.json(t, &found, "post", "search", "integration", "--in", ch)
		if len(found) > 0 {
			break
		}
		if time.Now().After(searchDeadline) {
			t.Fatalf("post search found nothing within %s; the server index may be disabled or lagging (last result: %+v)", searchIndexTimeout, found)
		}
		time.Sleep(searchPollInterval)
	}

	// Delete.
	e.ok(t, "post", "delete", reply.ID, "--yes")
	if r := e.run(t, "", "post", "get", reply.ID); r.code != 5 {
		t.Fatalf("get after delete: exit %d", r.code)
	}

	// Direct messages.
	e.ok(t, "send", "@me", "a note to myself")
	var bobMe *model.User
	bobMe, _, err := e.bob.GetMe(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	e.ok(t, "send", "@"+bobMe.Username, "hi bob")
}

func TestChannels(t *testing.T) {
	e := setup(t)
	name := unique("it-")
	e.ok(t, "channel", "create", name, "--private", "--purpose", "integration")
	e.ok(t, "channel", "create", name, "--private", "--exist-ok")
	var info struct {
		Name    string `json:"name"`
		Purpose string `json:"purpose"`
		Header  string `json:"header"`
	}
	e.json(t, &info, "channel", "get", name)
	if info.Name != name || info.Purpose != "integration" {
		t.Fatalf("channel get: %+v", info)
	}
	e.ok(t, "channel", "edit", name, "--header", "new header")
	e.json(t, &info, "channel", "get", e.team.Name+"/"+name)
	if info.Header != "new header" {
		t.Fatalf("after edit: %+v", info)
	}
	if out := e.ok(t, "channel", "list", "--type", "P"); !strings.Contains(out, name) {
		t.Fatalf("channel list: %s", out)
	}
	e.ok(t, "channel", "read", name)
	e.ok(t, "channel", "archive", name)
	e.ok(t, "channel", "unarchive", name)

	// Bookmarks (open question 4): unsupported servers answer with code 5.
	r := e.run(t, "", "--json", "bookmark", "add", name, "--url", "https://example.com/", "--name", "Example")
	switch r.code {
	case 0:
		var bm struct{ ID string }
		if err := json.Unmarshal([]byte(r.out), &bm); err != nil {
			t.Fatal(err)
		}
		e.ok(t, "bookmark", "edit", name, bm.ID, "--name", "Renamed")
		if out := e.ok(t, "bookmark", "list", name); !strings.Contains(out, "Renamed") {
			t.Fatalf("bookmark list: %s", out)
		}
		e.ok(t, "bookmark", "delete", name, bm.ID, "--yes")
	case 5:
		t.Logf("channel bookmarks are not supported: %s", r.err)
	default:
		t.Fatalf("bookmark add: exit %d %s", r.code, r.err)
	}
}

const (
	// watchEventTimeout bounds the wait for watch to print a posted message.
	watchEventTimeout = 30 * time.Second
	// watchStopTimeout bounds the wait for watch to exit after cancellation.
	watchStopTimeout = 10 * time.Second
	// searchIndexTimeout bounds the wait for the asynchronous search index.
	searchIndexTimeout = 30 * time.Second
	searchPollInterval = time.Second
)

func TestWatch(t *testing.T) {
	e := setup(t)
	out, errb := &syncBuffer{}, &syncBuffer{}
	e.app.In, e.app.Out, e.app.Err = strings.NewReader(""), out, errb
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- cli.Execute(ctx, e.app, []string{"watch", "town-square"}) }()
	defer func() {
		cancel()
		select {
		case code := <-done:
			if code != 0 {
				t.Errorf("watch exit %d: %s", code, errb.String())
			}
		case <-time.After(watchStopTimeout):
			t.Errorf("watch did not stop within %s after cancellation", watchStopTimeout)
		}
	}()

	townSquare, _, err := e.bob.GetChannelByName(context.Background(), "town-square", e.team.Id, "")
	if err != nil {
		t.Fatal(err)
	}
	// The connection needs a moment before it receives events; post until
	// one arrives rather than guessing a delay.
	deadline := time.Now().Add(watchEventTimeout)
	for !strings.Contains(out.String(), "watch me") {
		if time.Now().After(deadline) {
			t.Fatalf("watch printed nothing within %s\nstdout: %s\nstderr: %s", watchEventTimeout, out.String(), errb.String())
		}
		if _, _, err := e.bob.CreatePost(context.Background(), &model.Post{ChannelId: townSquare.Id, Message: "watch me"}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Second)
	}
}

type thread struct {
	ID            string `json:"id"`
	Channel       string `json:"channel"`
	ChannelType   string `json:"channel_type"`
	ReplyCount    int64  `json:"reply_count"`
	UnreadReplies int64  `json:"unread_replies"`
}

func findThread(list []thread, id string) (thread, bool) {
	for _, t := range list {
		if t.ID == id {
			return t, true
		}
	}
	return thread{}, false
}

func TestThreads(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	// Threads need Collapsed Reply Threads on the server (open question 3).
	cfg, _, err := e.admin.GetConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("CollapsedThreads on the server before the test: %s", *cfg.ServiceSettings.CollapsedThreads)
	on := model.CollapsedThreadsAlwaysOn
	if _, _, err := e.admin.PatchConfig(ctx, &model.Config{ServiceSettings: model.ServiceSettings{CollapsedThreads: &on}}); err != nil {
		t.Fatal(err)
	}

	reply := func(root post, text string) {
		t.Helper()
		if _, _, err := e.bob.CreatePost(ctx, &model.Post{ChannelId: root.ChannelID, RootId: root.ID, Message: text}); err != nil {
			t.Fatal(err)
		}
	}
	list := func(args ...string) []thread {
		t.Helper()
		var out []thread
		e.json(t, &out, append([]string{"thread", "list"}, args...)...)
		return out
	}

	var root post
	e.json(t, &root, "send", "town-square", "thread root")
	reply(root, "first reply")
	// Open question 4: does the author of the root follow it without asking?
	_, auto := findThread(list(), root.ID)
	t.Logf("root author follows the thread after a reply without thread follow: %v", auto)
	// Following marks the thread read on the server.
	e.ok(t, "thread", "follow", root.ID)
	if _, ok := findThread(list("--unread"), root.ID); ok {
		t.Fatal("thread still unread after thread follow")
	}
	reply(root, "second reply")

	got, ok := findThread(list("--unread"), root.ID)
	if !ok || got.UnreadReplies < 1 || got.ReplyCount != 2 || got.Channel != e.team.Name+"/town-square" || got.ChannelType != "O" {
		t.Fatalf("thread list --unread after a reply: %+v (found %v)", got, ok)
	}
	// Reading does not mark the thread read.
	e.ok(t, "post", "thread", root.ID)
	e.ok(t, "thread", "list")
	if _, ok := findThread(list("--unread"), root.ID); !ok {
		t.Fatal("reading marked the thread read")
	}
	e.ok(t, "thread", "read", root.ID)
	if _, ok := findThread(list("--unread"), root.ID); ok {
		t.Fatal("thread still unread after thread read")
	}

	e.ok(t, "thread", "unfollow", root.ID)
	if _, ok := findThread(list(), root.ID); ok {
		t.Fatal("thread listed after unfollow")
	}
	e.ok(t, "thread", "follow", root.ID)
	if _, ok := findThread(list(), root.ID); !ok {
		t.Fatal("thread not listed after follow")
	}

	reply(root, "third reply")
	if _, ok := findThread(list("--unread"), root.ID); !ok {
		t.Fatal("thread not unread after the third reply")
	}
	e.ok(t, "thread", "read", "--all")
	if _, ok := findThread(list("--unread"), root.ID); ok {
		t.Fatal("thread still unread after thread read --all")
	}

	// A thread in a direct channel, seen from a second team (open questions 1
	// and 2).
	bobMe, _, err := e.bob.GetMe(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	team2 := unique("team2-")
	if _, _, err := e.alice.CreateTeam(ctx, &model.Team{Name: team2, DisplayName: "Second team", Type: model.TeamOpen}); err != nil {
		t.Fatalf("create second team: %v", err)
	}
	var dmRoot post
	e.json(t, &dmRoot, "send", "@"+bobMe.Username, "direct root")
	e.ok(t, "thread", "follow", dmRoot.ID)
	reply(dmRoot, "direct reply")
	got, ok = findThread(list("--unread"), dmRoot.ID)
	if !ok || got.Channel != "@"+bobMe.Username || got.ChannelType != "D" {
		t.Fatalf("direct thread in thread list: %+v (found %v)", got, ok)
	}
	_, inTeam2 := findThread(list("--unread", "--team", team2), dmRoot.ID)
	t.Logf("direct-channel thread listed for the second team too: %v", inTeam2)
	e.ok(t, "thread", "read", dmRoot.ID, "--team", team2)
	if _, ok := findThread(list("--unread"), dmRoot.ID); ok {
		t.Fatal("direct thread still unread after thread read with the second team")
	}

	// Open question 3: what thread list does with threads turned off.
	off := model.CollapsedThreadsDisabled
	if _, _, err := e.admin.PatchConfig(ctx, &model.Config{ServiceSettings: model.ServiceSettings{CollapsedThreads: &off}}); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, _, err := e.admin.PatchConfig(ctx, &model.Config{ServiceSettings: model.ServiceSettings{CollapsedThreads: &on}}); err != nil {
			t.Error(err)
		}
	}()
	r := e.run(t, "", "thread", "list", "--json")
	t.Logf("thread list with CollapsedThreads disabled: exit %d, stdout %q, stderr %q", r.code, r.out, r.err)
}
