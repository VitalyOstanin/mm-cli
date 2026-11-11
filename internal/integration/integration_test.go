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
	"image"
	"image/png"
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
	r := e.run(t, alice.AuthToken+"\n", "auth", "login", url, "--token-stdin", "--name", "it", "--team", team.Name)
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
	e.ok(t, "auth", "list")

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
	e.json(t, &reply, "send", ch, "a reply", "--reply-to", sent.ID)
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
	// File search uses the same asynchronous server index as post search.
	var fsFound []struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		PostID string `json:"post_id"`
	}
	fileSearchDeadline := time.Now().Add(searchIndexTimeout)
	for {
		e.json(t, &fsFound, "file", "search", "a.txt", "--in", ch, "--ext", "txt")
		if len(fsFound) > 0 {
			break
		}
		if time.Now().After(fileSearchDeadline) {
			t.Fatalf("file search found nothing within %s; the server index may be disabled or lagging", searchIndexTimeout)
		}
		time.Sleep(searchPollInterval)
	}
	if fsFound[0].Name != "a.txt" || fsFound[0].PostID != sent.ID {
		t.Fatalf("file search: %+v (want a.txt of post %s)", fsFound, sent.ID)
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
		e.ok(t, "file", "remove", sent.ID, got.Files[1].ID, "--confirm")
		e.json(t, &got, "post", "get", sent.ID)
		if len(got.Files) != 1 || got.Files[0].Name != "c.txt" {
			t.Fatalf("after replace and remove: %+v", got.Files)
		}
	}

	// Pins and saved messages.
	e.ok(t, "post", "pin", sent.ID)
	var pins []post
	e.json(t, &pins, "channel", "pinned", ch)
	if len(pins) != 1 || pins[0].ID != sent.ID {
		t.Fatalf("pin list: %+v", pins)
	}
	e.ok(t, "post", "unpin", sent.ID)
	e.ok(t, "post", "save", sent.ID)
	var saved []post
	e.json(t, &saved, "post", "saved")
	if len(saved) != 1 || saved[0].ID != sent.ID {
		t.Fatalf("saved list: %+v", saved)
	}
	e.ok(t, "post", "unsave", sent.ID)

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
	e.ok(t, "post", "delete", reply.ID, "--confirm")
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
	e.json(t, &info, "channel", "get", e.team.Name+":"+name)
	if info.Header != "new header" {
		t.Fatalf("after edit: %+v", info)
	}
	if out := e.ok(t, "channel", "list", "--type", "P"); !strings.Contains(out, name) {
		t.Fatalf("channel list: %s", out)
	}
	e.ok(t, "channel", "mark-read", name)
	e.ok(t, "channel", "archive", name)
	e.ok(t, "channel", "unarchive", name)

	// Bookmarks (open question 4): unsupported servers answer with code 5.
	r := e.run(t, "", "--json", "bookmark", "add", name, "--link-url", "https://example.com/", "--display-name", "Example")
	switch r.code {
	case 0:
		var bm struct{ ID string }
		if err := json.Unmarshal([]byte(r.out), &bm); err != nil {
			t.Fatal(err)
		}
		e.ok(t, "bookmark", "edit", name, bm.ID, "--display-name", "Renamed")
		if out := e.ok(t, "bookmark", "list", name); !strings.Contains(out, "Renamed") {
			t.Fatalf("bookmark list: %s", out)
		}
		e.ok(t, "bookmark", "delete", name, bm.ID, "--confirm")
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
	if !ok || got.UnreadReplies < 1 || got.ReplyCount != 2 || got.Channel != e.team.Name+":town-square" || got.ChannelType != "O" {
		t.Fatalf("thread list --unread after a reply: %+v (found %v)", got, ok)
	}
	// Reading does not mark the thread read.
	e.ok(t, "post", "thread", root.ID)
	e.ok(t, "thread", "list")
	if _, ok := findThread(list("--unread"), root.ID); !ok {
		t.Fatal("reading marked the thread read")
	}
	e.ok(t, "thread", "mark-read", root.ID)
	if _, ok := findThread(list("--unread"), root.ID); ok {
		t.Fatal("thread still unread after thread mark-read")
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
	e.ok(t, "thread", "mark-read", "--all")
	if _, ok := findThread(list("--unread"), root.ID); ok {
		t.Fatal("thread still unread after thread mark-read --all")
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
	e.ok(t, "thread", "mark-read", dmRoot.ID, "--team", team2)
	if _, ok := findThread(list("--unread"), dmRoot.ID); ok {
		t.Fatal("direct thread still unread after thread mark-read with the second team")
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

type scheduled struct {
	ID          string `json:"id"`
	Channel     string `json:"channel"`
	Message     string `json:"message"`
	ScheduledAt string `json:"scheduled_at"`
	Repeat      string `json:"repeat"`
	Error       string `json:"error"`
}

func findScheduled(list []scheduled, id string) (scheduled, bool) {
	for _, s := range list {
		if s.ID == id {
			return s, true
		}
	}
	return scheduled{}, false
}

func TestScheduled(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	cfg, _, err := e.admin.GetConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("ServiceSettings.ScheduledPosts: %v", *cfg.ServiceSettings.ScheduledPosts)

	at := time.Now().Add(time.Hour).UTC().Truncate(time.Minute).Format(time.RFC3339)
	r := e.run(t, "", "--json", "send", "town-square", "scheduled text", "--schedule", at)
	unavailable := r.code == 5 && strings.Contains(r.err, "not available on this server")
	// MM_TEST_SCHEDULED states what the server supports, so that neither
	// case passes by skipping: "available" needs the whole chain below,
	// "unavailable" (a server without a license) needs exit code 5.
	switch want := os.Getenv("MM_TEST_SCHEDULED"); want {
	case "unavailable":
		if !unavailable {
			t.Fatalf("send --schedule: exit %d, want 5 \"not available\"\n%s%s", r.code, r.out, r.err)
		}
		if l := e.run(t, "", "scheduled", "list"); l.code != 5 {
			t.Fatalf("scheduled list: exit %d, want 5\n%s%s", l.code, l.out, l.err)
		}
		return
	case "available":
		if unavailable {
			t.Fatalf("scheduled messages are not available on this server: %s", strings.TrimSpace(r.err))
		}
	case "":
		if unavailable {
			t.Skipf("scheduled messages are not available on this server (set MM_TEST_SCHEDULED): %s", strings.TrimSpace(r.err))
		}
	default:
		t.Fatalf("MM_TEST_SCHEDULED=%q: use available or unavailable", want)
	}
	if r.code != 0 {
		t.Fatalf("send --schedule: exit %d\n%s%s", r.code, r.out, r.err)
	}
	var sp scheduled
	if err := json.Unmarshal([]byte(r.out), &sp); err != nil {
		t.Fatal(err)
	}
	if sp.ScheduledAt != at || sp.Channel != e.team.Name+":town-square" {
		t.Fatalf("scheduled: %+v", sp)
	}
	list := func() []scheduled {
		t.Helper()
		var out []scheduled
		e.json(t, &out, "scheduled", "list")
		return out
	}
	if _, ok := findScheduled(list(), sp.ID); !ok {
		t.Fatal("scheduled message missing from scheduled list")
	}

	e.ok(t, "scheduled", "edit", sp.ID, "edited text")
	later := time.Now().Add(2 * time.Hour).UTC().Truncate(time.Minute).Format(time.RFC3339)
	e.ok(t, "scheduled", "reschedule", sp.ID, later)
	got, _ := findScheduled(list(), sp.ID)
	if got.Message != "edited text" || got.ScheduledAt != later {
		t.Fatalf("after edit and reschedule: %+v", got)
	}

	var sent post
	e.json(t, &sent, "scheduled", "send", sp.ID, "--confirm")
	if sent.Message != "edited text" {
		t.Fatalf("sent: %+v", sent)
	}
	var recent []post
	e.json(t, &recent, "messages", "town-square", "-n", "5")
	if len(recent) == 0 || recent[len(recent)-1].ID != sent.ID {
		t.Fatalf("sent message not in the channel: %+v", recent)
	}
	if _, ok := findScheduled(list(), sp.ID); ok {
		t.Fatal("scheduled message still listed after scheduled send")
	}

	var second scheduled
	e.json(t, &second, "send", "town-square", "to delete", "--schedule", at)
	e.ok(t, "scheduled", "delete", second.ID, "--confirm")
	if _, ok := findScheduled(list(), second.ID); ok {
		t.Fatal("scheduled message still listed after scheduled delete")
	}

	// Open question 1: does the server accept weekly repeats? The test user
	// has no profile time zone, so set one first.
	zone := model.StringMap{"useAutomaticTimezone": "false", "manualTimezone": "Europe/Berlin", "automaticTimezone": ""}
	me, _, err := e.alice.GetMe(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.alice.PatchUser(ctx, me.Id, &model.UserPatch{Timezone: zone}); err != nil {
		t.Fatal(err)
	}
	r = e.run(t, "", "--json", "send", "town-square", "weekly text", "--schedule", "monday", "--repeat-weekly")
	t.Logf("weekly: exit %d, %s%s", r.code, strings.TrimSpace(r.out), strings.TrimSpace(r.err))
	if r.code == 0 {
		var weekly scheduled
		if err := json.Unmarshal([]byte(r.out), &weekly); err != nil || weekly.Repeat != "weekly" {
			t.Fatalf("weekly: %v %+v", err, weekly)
		}
		if r := e.run(t, "", "scheduled", "send", weekly.ID, "--confirm"); r.code != 2 {
			t.Fatalf("scheduled send of a weekly message: exit %d, want 2", r.code)
		}
		e.ok(t, "scheduled", "delete", weekly.ID, "--confirm")
	}
}

func TestProfileAndStatus(t *testing.T) {
	e := setup(t)
	var cur struct {
		Name    string `json:"name"`
		Current bool   `json:"current"`
	}
	e.json(t, &cur, "auth", "current")
	if cur.Name != "it" {
		t.Fatalf("auth current: %+v", cur)
	}

	var p struct {
		Username string `json:"username"`
		Position string `json:"position"`
		Timezone string `json:"timezone"`
	}
	e.json(t, &p, "profile", "edit", "--position", "Release manager", "--timezone", "Asia/Tokyo")
	if p.Position != "Release manager" || p.Timezone != "Asia/Tokyo" {
		t.Fatalf("profile edit: %+v", p)
	}
	e.json(t, &p, "profile", "get")
	if p.Position != "Release manager" || p.Timezone != "Asia/Tokyo" {
		t.Fatalf("profile get: %+v", p)
	}

	type status struct {
		Status       string `json:"status"`
		DNDEndAt     string `json:"dnd_end_at"`
		CustomStatus *struct {
			Emoji     string `json:"emoji"`
			Text      string `json:"text"`
			ExpiresAt string `json:"expires_at"`
		} `json:"custom_status"`
	}
	e.ok(t, "status", "set", "dnd", "--until", "1h")
	e.ok(t, "status", "custom", "set", "In a meeting", "--emoji", "calendar", "--clear-after", "today")
	var sts []status
	e.json(t, &sts, "status", "get")
	if len(sts) != 1 || sts[0].Status != "dnd" || sts[0].DNDEndAt == "" || sts[0].CustomStatus == nil ||
		sts[0].CustomStatus.Text != "In a meeting" || sts[0].CustomStatus.ExpiresAt == "" {
		t.Fatalf("status get: %+v", sts)
	}
	e.ok(t, "status", "custom", "clear")
	e.ok(t, "status", "set", "online")
	sts = nil
	e.json(t, &sts, "status", "get")
	if len(sts) != 1 || sts[0].Status != "online" || sts[0].CustomStatus != nil {
		t.Fatalf("status get after clearing: %+v", sts)
	}

	img := writeFile(t, e.dir, "pic.png", string(tinyPNG(t)))
	e.ok(t, "profile", "picture", "set", img)
	e.ok(t, "profile", "picture", "reset")
}

// tinyPNG returns a 1x1 PNG image.
func tinyPNG(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
