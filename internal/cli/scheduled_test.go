package cli

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	// Tests load named zones on any system, as the binary does.
	_ "time/tzdata"

	"github.com/mattermost/mattermost/server/public/model"

	"github.com/VitalyOstanin/mm-cli/internal/clierr"
	"github.com/VitalyOstanin/mm-cli/internal/fakemm"
	"github.com/VitalyOstanin/mm-cli/internal/render"
)

var (
	sched1 = fakemm.ID("sched1")
	sched2 = fakemm.ID("sched2")
	sched3 = fakemm.ID("sched3")
)

const (
	schedulePath = "/api/v4/posts/schedule"
	meteam       = "/api/v4/users/" + "me"
)

func scheduledPath(team string) string { return "/api/v4/posts/scheduled/team/" + team }

// mustZone loads a named time zone or fails the test.
func mustZone(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func TestParseWhen(t *testing.T) {
	msk := mustZone(t, "Europe/Moscow")
	tue := time.Date(2026, 3, 3, 12, 0, 0, 0, time.UTC)
	mon := time.Date(2026, 3, 2, 12, 0, 0, 0, time.UTC)
	sun := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		when string
		now  time.Time
		loc  *time.Location
		want time.Time
	}{
		{"tomorrow", tue, time.UTC, time.Date(2026, 3, 4, 9, 0, 0, 0, time.UTC)},
		{"monday", tue, time.UTC, time.Date(2026, 3, 9, 9, 0, 0, 0, time.UTC)},
		{"next-monday", tue, time.UTC, time.Date(2026, 3, 9, 9, 0, 0, 0, time.UTC)},
		{"Monday", mon, time.UTC, time.Date(2026, 3, 9, 9, 0, 0, 0, time.UTC)},
		{"monday", sun, time.UTC, time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)},
		{"tomorrow", sun, time.UTC, time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)},
		{"tomorrow", tue, msk, time.Date(2026, 3, 4, 9, 0, 0, 0, msk)},
		{"2026-03-05 14:30", tue, msk, time.Date(2026, 3, 5, 14, 30, 0, 0, msk)},
		{"2026-03-05T14:30:00+03:00", tue, time.UTC, time.Date(2026, 3, 5, 11, 30, 0, 0, time.UTC)},
	}
	for _, tc := range tests {
		got, err := parseWhen(tc.when, tc.now, tc.loc)
		if err != nil || !got.Equal(tc.want) {
			t.Errorf("parseWhen(%q, %s, %s) = %s, %v; want %s", tc.when, tc.now, tc.loc, got, err, tc.want)
		}
	}
	for _, bad := range []string{"2026-03-03 11:00", "2026-03-03T12:00:00Z", "yesterday", "2026-13-01 10:00", ""} {
		if _, err := parseWhen(bad, tue, time.UTC); clierr.ExitCode(err) != clierr.CodeUsage {
			t.Errorf("parseWhen(%q): %v, want a usage error", bad, err)
		}
		if bad == "yesterday" || bad == "" || bad == "2026-13-01 10:00" {
			if whenSyntax(bad) == nil {
				t.Errorf("whenSyntax(%q) accepted it", bad)
			}
		}
	}
}

// serveMe answers GetMe with a profile time zone.
func serveMe(s *fakemm.Server, zone string) {
	s.JSON(http.MethodGet, meteam, 200, &model.User{Id: meID, Username: "alice",
		Timezone: model.StringMap{"useAutomaticTimezone": "false", "manualTimezone": zone}})
}

// serveScheduledChannels serves the user's teams and channels that scheduled
// messages are rendered with.
func serveScheduledChannels(s *fakemm.Server) {
	s.JSON(http.MethodGet, "/api/v4/users/"+meID+"/teams", 200, []*model.Team{{Id: teamID, Name: "myteam"}, {Id: team2ID, Name: "team2"}})
	chans := []*model.Channel{
		{Id: townSq, Name: "town-square", Type: model.ChannelTypeOpen, TeamId: teamID},
		{Id: dmChan, Name: meID + "__" + bobID, Type: model.ChannelTypeDirect},
	}
	s.JSON(http.MethodGet, "/api/v4/users/"+meID+"/channels", 200, chans)
	// One scheduled message is shown by looking up its own channel.
	for _, ch := range chans {
		s.JSON(http.MethodGet, "/api/v4/channels/"+ch.Id, 200, ch)
	}
}

func TestSendSchedule(t *testing.T) {
	app, s := newTestApp(t)
	serveTownSquare(s)
	serveMe(s, "Europe/Moscow")
	var got model.ScheduledPost
	s.Handle(http.MethodPost, schedulePath, func(w http.ResponseWriter, r *http.Request) {
		got = model.ScheduledPost{}
		_ = json.NewDecoder(r.Body).Decode(&got)
		got.Id = sched1
		fakemm.WriteJSON(w, http.StatusCreated, &got)
	})

	r := run(app, "send", "town-square", "standup", "--schedule", "tomorrow", "--repeat-weekly")
	mustCode(t, r, 0)
	msk := mustZone(t, "Europe/Moscow")
	want := time.Date(2026, 3, 5, 9, 0, 0, 0, msk)
	if got.ChannelId != townSq || got.Message != "standup" || got.ScheduledAt != want.UnixMilli() ||
		got.RepeatType != "weekly" || got.RepeatTimezone != "Europe/Moscow" {
		t.Fatalf("request: %+v", &got)
	}
	if r.out != "Scheduled: "+sched1+" for 2026-03-05 06:00 UTC\n" {
		t.Fatalf("output: %q", r.out)
	}
	if s.Count(http.MethodPost, "/api/v4/posts") != 0 {
		t.Fatal("the message was sent instead of scheduled")
	}

	// RFC 3339 needs no profile, and a reply keeps its root.
	root := fakemm.ID("root")
	before := s.Count(http.MethodGet, meteam)
	serveScheduledChannels(s)
	r = run(app, "send", "town-square", "later", "--reply-to", root, "--schedule", "2026-03-04T13:00:00Z", "--json")
	mustCode(t, r, 0)
	if s.Count(http.MethodGet, meteam) != before {
		t.Fatal("RFC 3339 looked up the profile time zone")
	}
	if got.RootId != root || got.RepeatType != "" || got.RepeatTimezone != "" {
		t.Fatalf("request: %+v", &got)
	}
	var out render.Scheduled
	if err := json.Unmarshal([]byte(r.out), &out); err != nil || out.ID != sched1 || out.Channel != "myteam:town-square" ||
		out.ScheduledAt != "2026-03-04T13:00:00Z" || out.Files == nil {
		t.Fatalf("json: %v %+v\n%s", err, out, r.out)
	}
}

// TestSendScheduleUnknownProfileZone: a profile zone that does not load
// stops the command instead of falling back to the local zone.
func TestSendScheduleUnknownProfileZone(t *testing.T) {
	app, s := newTestApp(t)
	serveTownSquare(s)
	serveMe(s, "Mars/Olympus")
	r := run(app, "send", "town-square", "standup", "--schedule", "tomorrow")
	mustCode(t, r, 1)
	if !strings.Contains(r.err, `"Mars/Olympus"`) || !strings.Contains(r.err, "--timezone") {
		t.Fatalf("stderr: %s", r.err)
	}
	if s.Count(http.MethodPost, schedulePath) != 0 {
		t.Fatal("scheduled despite the unknown zone")
	}
}

// TestSendSchedulePastTimeUploadsNothing: a past time in the profile's zone
// is refused before any file is uploaded.
func TestSendSchedulePastTimeUploadsNothing(t *testing.T) {
	app, s := newTestApp(t)
	serveTownSquare(s)
	serveMe(s, "Europe/Moscow")
	s.JSON(http.MethodPost, "/api/v4/files", 201, &model.FileUploadResponse{FileInfos: []*model.FileInfo{{Id: fakemm.ID("file")}}})
	f := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := run(app, "send", "town-square", "x", "-f", f, "--schedule", "2026-03-01 10:00")
	mustCode(t, r, 2)
	if !strings.Contains(r.err, "not in the future") {
		t.Fatal(r.err)
	}
	if n := s.Count(http.MethodPost, "/api/v4/files"); n != 0 {
		t.Fatalf("%d files uploaded", n)
	}
}

func TestSendScheduleUsage(t *testing.T) {
	app, s := newTestApp(t)
	for _, args := range [][]string{
		{"send", "town-square", "x", "--repeat-weekly"},
		{"send", "town-square", "x", "--schedule", "soon"},
		{"send", "town-square", "x", "--schedule", ""},
		{"send", "town-square", "-f", "go.mod", "--schedule", "tomorrow", "--repeat-weekly"},
		{"post", "create", "town-square", "x", "--schedule", "2026-03-04T11:00:00Z"},
	} {
		r := run(app, args...)
		mustCode(t, r, 2)
	}
	if n := s.Count(http.MethodPost, schedulePath) + s.Count(http.MethodPost, "/api/v4/files"); n != 0 {
		t.Fatalf("%d requests sent", n)
	}

	// Without a time zone in the profile or a named local zone a weekly
	// message cannot be scheduled.
	serveTownSquare(s)
	serveMe(s, "")
	app.Location = time.Local
	if time.Local.String() == "Local" {
		mustCode(t, run(app, "send", "town-square", "x", "--schedule", "tomorrow", "--repeat-weekly"), 2)
	}
}

func TestSendScheduleNotAvailable(t *testing.T) {
	app, s := newTestApp(t)
	serveTownSquare(s)
	s.Handle(http.MethodPost, schedulePath, func(w http.ResponseWriter, _ *http.Request) {
		fakemm.WriteJSON(w, 400, map[string]any{"id": "api.scheduled_posts.license_error", "message": "no license", "status_code": 400})
	})
	r := run(app, "send", "town-square", "x", "--schedule", "2026-03-05T10:00:00Z")
	mustCode(t, r, 5)
	if !strings.Contains(r.err, "not available on this server") {
		t.Fatalf("stderr: %s", r.err)
	}
}

// serveScheduledList serves scheduled messages: one in each team, one failed
// weekly one in a direct channel, listed again with the second team.
func serveScheduledList(s *fakemm.Server) {
	serveScheduledChannels(s)
	dm := &model.ScheduledPost{Id: sched3, Draft: model.Draft{ChannelId: dmChan, Message: "to bob"}, ScheduledAt: now.Add(time.Hour).UnixMilli(),
		RepeatType: "weekly", ErrorCode: "user_deleted"}
	s.Handle(http.MethodGet, scheduledPath(teamID), func(w http.ResponseWriter, r *http.Request) {
		m := map[string][]*model.ScheduledPost{teamID: {{Id: sched1, Draft: model.Draft{ChannelId: townSq, Message: "later"},
			ScheduledAt: now.Add(2 * time.Hour).UnixMilli()}}}
		if r.URL.Query().Get("includeDirectChannels") == "true" {
			m[directChannelsKey] = []*model.ScheduledPost{dm}
		}
		fakemm.WriteJSON(w, 200, m)
	})
	s.Handle(http.MethodGet, scheduledPath(team2ID), func(w http.ResponseWriter, r *http.Request) {
		m := map[string][]*model.ScheduledPost{team2ID: {{Id: sched2, Draft: model.Draft{ChannelId: fakemm.ID("gone"), Message: "elsewhere"},
			ScheduledAt: now.Add(3 * time.Hour).UnixMilli()}}}
		if r.URL.Query().Get("includeDirectChannels") == "true" {
			m[directChannelsKey] = []*model.ScheduledPost{dm}
		}
		fakemm.WriteJSON(w, 200, m)
	})
}

func TestScheduledList(t *testing.T) {
	app, s := newTestApp(t)
	serveScheduledList(s)
	serveTownSquare(s)
	cfg, err := app.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Servers["test"].Team = ""
	if err := app.Store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	r := run(app, "scheduled", "list")
	mustCode(t, r, 0)
	want := "2026-03-04 13:00  @bob  id " + sched3 + ", weekly, failed: user_deleted\nto bob\n\n" +
		"2026-03-04 14:00  #myteam:town-square  id " + sched1 + "\nlater\n\n" +
		"2026-03-04 15:00  " + fakemm.ID("gone") + "  id " + sched2 + "\nelsewhere\n"
	if r.out != want {
		t.Fatalf("got:\n%s\nwant:\n%s", r.out, want)
	}
	if q := s.Last(http.MethodGet, scheduledPath(team2ID)).Query; q.Get("includeDirectChannels") != "false" {
		t.Fatalf("second team asked for direct channels again: %v", q)
	}

	r = run(app, "scheduled", "list", "--team", "myteam", "--json")
	mustCode(t, r, 0)
	var out []render.Scheduled
	if err := json.Unmarshal([]byte(r.out), &out); err != nil || len(out) != 2 || out[0].ID != sched3 ||
		out[0].Error != "user_deleted" || out[0].Repeat != "weekly" || out[0].Channel != "@bob" || out[1].Channel != "myteam:town-square" {
		t.Fatalf("json: %v %+v", err, out)
	}
}

func TestScheduledListEmpty(t *testing.T) {
	app, s := newTestApp(t)
	serveTownSquare(s)
	s.JSON(http.MethodGet, scheduledPath(teamID), 200, map[string][]*model.ScheduledPost{teamID: {}})
	r := run(app, "scheduled", "list", "--json")
	mustCode(t, r, 0)
	if strings.TrimSpace(r.out) != "[]" {
		t.Fatalf("output: %q", r.out)
	}
}

func TestScheduledEditAndReschedule(t *testing.T) {
	app, s := newTestApp(t)
	serveScheduledList(s)
	serveMe(s, "UTC")
	var got model.ScheduledPost
	for _, id := range []string{sched1, sched3} {
		s.Handle(http.MethodPut, schedulePath+"/"+id, func(w http.ResponseWriter, r *http.Request) {
			got = model.ScheduledPost{}
			_ = json.NewDecoder(r.Body).Decode(&got)
			fakemm.WriteJSON(w, http.StatusCreated, &got)
		})
	}

	r := run(app, "scheduled", "edit", sched1, "new text")
	mustCode(t, r, 0)
	if got.Id != sched1 || got.Message != "new text" || got.ScheduledAt != now.Add(2*time.Hour).UnixMilli() || got.ChannelId != townSq {
		t.Fatalf("request: %+v", &got)
	}
	if !strings.HasPrefix(r.out, "Edited: "+sched1) {
		t.Fatalf("output: %q", r.out)
	}

	r = run(app, "scheduled", "reschedule", sched1, "tomorrow", "--json")
	mustCode(t, r, 0)
	if got.Message != "later" || got.ScheduledAt != time.Date(2026, 3, 5, 9, 0, 0, 0, time.UTC).UnixMilli() {
		t.Fatalf("request: %+v", &got)
	}
	var out render.Scheduled
	if err := json.Unmarshal([]byte(r.out), &out); err != nil || out.ID != sched1 {
		t.Fatalf("json: %v %s", err, r.out)
	}

	puts := s.Count(http.MethodPut, schedulePath+"/"+sched3)
	mustCode(t, run(app, "scheduled", "reschedule", sched3, "tomorrow"), 2)
	mustCode(t, run(app, "scheduled", "edit", sched1, ""), 2)
	mustCode(t, run(app, "scheduled", "reschedule", sched1, "someday"), 2)
	mustCode(t, run(app, "scheduled", "edit", fakemm.ID("missing"), "x"), 5)
	if s.Count(http.MethodPut, schedulePath+"/"+sched3) != puts {
		t.Fatal("a failed message was rescheduled")
	}
}

// TestScheduledEditJSONLooksUpOneChannel: printing one scheduled message
// reads its channel by id and never lists all the user's channels.
func TestScheduledEditJSONLooksUpOneChannel(t *testing.T) {
	app, s := newTestApp(t)
	serveScheduledList(s)
	serveMe(s, "UTC")
	s.JSON(http.MethodPost, "/api/v4/users/ids", 200, []*model.User{{Id: meID, Username: "alice"}, {Id: bobID, Username: "bob"}})
	for _, id := range []string{sched1, sched3} {
		s.Handle(http.MethodPut, schedulePath+"/"+id, func(w http.ResponseWriter, r *http.Request) {
			var got model.ScheduledPost
			_ = json.NewDecoder(r.Body).Decode(&got)
			fakemm.WriteJSON(w, http.StatusOK, &got)
		})
	}
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"edit", sched1, "x", "--json"}, "myteam:town-square"},
		{[]string{"reschedule", sched1, "tomorrow", "--json"}, "myteam:town-square"},
		{[]string{"edit", sched3, "x", "--json"}, "@bob"},
	} {
		r := run(app, append([]string{"scheduled"}, c.args...)...)
		mustCode(t, r, 0)
		var out render.Scheduled
		if err := json.Unmarshal([]byte(r.out), &out); err != nil || out.Channel != c.want {
			t.Fatalf("%v: %v %s", c.args, err, r.out)
		}
	}
	if n := s.Count(http.MethodGet, "/api/v4/users/"+meID+"/channels"); n != 0 {
		t.Fatalf("listed the user's channels %d times", n)
	}
}

func TestScheduledSend(t *testing.T) {
	app, s := newTestApp(t)
	serveScheduledList(s)
	var order []string
	var posted model.Post
	s.Handle(http.MethodPost, "/api/v4/posts", func(w http.ResponseWriter, r *http.Request) {
		order = append(order, "create")
		_ = json.NewDecoder(r.Body).Decode(&posted)
		posted.Id = fakemm.ID("sent")
		posted.UserId = meID
		fakemm.WriteJSON(w, http.StatusCreated, &posted)
	})
	s.Handle(http.MethodDelete, schedulePath+"/"+sched1, func(w http.ResponseWriter, _ *http.Request) {
		order = append(order, "delete")
		fakemm.WriteJSON(w, http.StatusCreated, &model.ScheduledPost{Id: sched1})
	})

	mustCode(t, run(app, "scheduled", "send", sched1), 2)
	if len(order) != 0 {
		t.Fatal("sent without --confirm")
	}
	r := run(app, "scheduled", "send", sched1, "--confirm", "--json")
	mustCode(t, r, 0)
	if strings.Join(order, ",") != "create,delete" || posted.ChannelId != townSq || posted.Message != "later" {
		t.Fatalf("order %v, post %+v", order, &posted)
	}
	var out render.Post
	if err := json.Unmarshal([]byte(r.out), &out); err != nil || out.ID != fakemm.ID("sent") {
		t.Fatalf("json: %v %s", err, r.out)
	}

	// A weekly, failed message cannot be sent now.
	order = nil
	mustCode(t, run(app, "scheduled", "send", sched3, "--confirm"), 2)
	if len(order) != 0 {
		t.Fatalf("requests: %v", order)
	}

	// When posting fails, the scheduled message stays.
	s.Handle(http.MethodPost, "/api/v4/posts", func(w http.ResponseWriter, _ *http.Request) {
		order = append(order, "create")
		fakemm.AppError(w, http.StatusForbidden, "no permission")
	})
	mustCode(t, run(app, "scheduled", "send", sched1, "--confirm"), 4)
	if strings.Join(order, ",") != "create" {
		t.Fatalf("requests: %v", order)
	}
}

func TestScheduledSendDeleteFails(t *testing.T) {
	app, s := newTestApp(t)
	serveScheduledList(s)
	s.Handle(http.MethodPost, "/api/v4/posts", func(w http.ResponseWriter, _ *http.Request) {
		fakemm.WriteJSON(w, http.StatusCreated, &model.Post{Id: fakemm.ID("sent"), ChannelId: townSq, UserId: meID})
	})
	s.Handle(http.MethodDelete, schedulePath+"/"+sched1, func(w http.ResponseWriter, _ *http.Request) {
		fakemm.AppError(w, http.StatusInternalServerError, "boom")
	})
	r := run(app, "scheduled", "send", sched1, "--confirm")
	mustCode(t, r, 1)
	if r.out != "Sent: "+fakemm.ID("sent")+"\n" || !strings.Contains(r.err, "not deleted") {
		t.Fatalf("stdout %q, stderr %q", r.out, r.err)
	}
}

func TestScheduledDelete(t *testing.T) {
	app, s := newTestApp(t)
	s.JSON(http.MethodDelete, schedulePath+"/"+sched1, http.StatusCreated, &model.ScheduledPost{Id: sched1})
	s.Handle(http.MethodDelete, schedulePath+"/"+sched2, func(w http.ResponseWriter, _ *http.Request) {
		fakemm.AppError(w, http.StatusNotFound, "not there")
	})

	mustCode(t, run(app, "scheduled", "delete", sched1), 2)
	mustCode(t, run(app, "scheduled", "delete", sched1, "bad", "--confirm"), 2)
	if s.Count(http.MethodDelete, schedulePath+"/"+sched1) != 0 {
		t.Fatal("deleted before the checks")
	}
	r := run(app, "scheduled", "delete", sched1, sched2, sched3, "--confirm", "--json")
	mustCode(t, r, 5)
	var out []actionResult
	if err := json.Unmarshal([]byte(r.out), &out); err != nil || len(out) != 1 || out[0].ID != sched1 || out[0].Action != "deleted" {
		t.Fatalf("json: %v %s", err, r.out)
	}
	if !strings.Contains(r.err, sched2) || s.Count(http.MethodDelete, schedulePath+"/"+sched3) != 0 {
		t.Fatalf("stderr %s", r.err)
	}
}

// A direct channel with yourself is named <id>__<id>; it shows as @username,
// not by id.
func TestScheduledListSelfDM(t *testing.T) {
	app, s := newTestApp(t)
	self := fakemm.ID("selfdm")
	s.JSON(http.MethodGet, "/api/v4/teams/name/myteam", 200, &model.Team{Id: teamID, Name: "myteam"})
	s.JSON(http.MethodGet, "/api/v4/users/"+meID+"/teams", 200, []*model.Team{{Id: teamID, Name: "myteam"}})
	s.JSON(http.MethodGet, "/api/v4/users/"+meID+"/channels", 200, []*model.Channel{{Id: self, Name: meID + "__" + meID, Type: model.ChannelTypeDirect}})
	s.JSON(http.MethodPost, "/api/v4/users/ids", 200, []*model.User{{Id: meID, Username: "alice"}})
	s.JSON(http.MethodGet, scheduledPath(teamID), 200, map[string][]*model.ScheduledPost{directChannelsKey: {
		{Id: sched1, Draft: model.Draft{ChannelId: self, Message: "note"}, ScheduledAt: now.Add(time.Hour).UnixMilli()}}})
	var out []render.Scheduled
	r := run(app, "scheduled", "list", "--json")
	mustCode(t, r, 0)
	if err := json.Unmarshal([]byte(r.out), &out); err != nil || len(out) != 1 || out[0].Channel != "@alice" {
		t.Fatalf("json: %v %s", err, r.out)
	}
}

// TestScheduledNoTeams: without a team the server cannot list scheduled
// messages; mm-cli says so instead of printing [].
func TestScheduledNoTeams(t *testing.T) {
	app, s := newTestApp(t)
	cfg, _ := app.Store.Load()
	cfg.Servers["test"].Team = ""
	if err := app.Store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	s.JSON(http.MethodGet, "/api/v4/users/"+meID+"/teams", 200, []*model.Team{})
	r := run(app, "scheduled", "list", "--json")
	mustCode(t, r, clierr.CodeNotFound)
	if !strings.Contains(r.err, "no team") {
		t.Fatal(r.err)
	}
}

// scheduledFileIDs decodes a scheduled post request and returns its file_ids
// and the ids in metadata.files; hasFiles tells whether metadata.files is
// present at all.
func scheduledFileIDs(t *testing.T, body []byte) (ids, metaIDs []string, hasFiles bool) {
	t.Helper()
	var sp model.ScheduledPost
	if err := json.Unmarshal(body, &sp); err != nil {
		t.Fatal(err)
	}
	if sp.Metadata != nil {
		hasFiles = sp.Metadata.Files != nil
		for _, f := range sp.Metadata.Files {
			metaIDs = append(metaIDs, f.Id)
		}
	}
	return sp.FileIds, metaIDs, hasFiles
}

// TestSendScheduleFilesCarryMetadata: the server broadcasts a scheduled post
// as sent, and the app lists files from metadata.files, so the request must
// carry the file records, not only the ids.
func TestSendScheduleFilesCarryMetadata(t *testing.T) {
	app, s := newTestApp(t)
	serveSend(t, s)
	serveMe(s, "UTC")
	s.Handle(http.MethodPost, schedulePath, func(w http.ResponseWriter, r *http.Request) {
		var sp model.ScheduledPost
		_ = json.NewDecoder(r.Body).Decode(&sp)
		sp.Id = sched1
		fakemm.WriteJSON(w, http.StatusCreated, &sp)
	})
	files := writeFiles(t, "a.txt", "b.pdf")
	mustCode(t, run(app, "send", "@me", "two files", "-f", files[0], "-f", files[1], "--schedule", "tomorrow"), 0)
	ids, meta, _ := scheduledFileIDs(t, s.Last(http.MethodPost, schedulePath).Body)
	want := []string{fakemm.ID("file1"), fakemm.ID("file2")}
	if strings.Join(ids, ",") != strings.Join(want, ",") || strings.Join(meta, ",") != strings.Join(want, ",") {
		t.Fatalf("file_ids %v, metadata.files %v; want %v", ids, meta, want)
	}

	mustCode(t, run(app, "send", "@me", "no files", "--schedule", "tomorrow"), 0)
	ids, meta, has := scheduledFileIDs(t, s.Last(http.MethodPost, schedulePath).Body)
	if len(ids) != 0 || len(meta) != 0 || has {
		t.Fatalf("file_ids %v, metadata.files %v (present %v)", ids, meta, has)
	}
}

// TestScheduledEditKeepsFileMetadata: edit and reschedule send back the
// scheduled post as the server listed it, with its metadata.files.
func TestScheduledEditKeepsFileMetadata(t *testing.T) {
	app, s := newTestApp(t)
	serveScheduledList(s)
	serveMe(s, "UTC")
	f1, f2 := fakemm.ID("file1"), fakemm.ID("file2")
	s.Handle(http.MethodGet, scheduledPath(teamID), func(w http.ResponseWriter, r *http.Request) {
		sp := &model.ScheduledPost{Id: sched1, ScheduledAt: now.Add(2 * time.Hour).UnixMilli(),
			Draft: model.Draft{ChannelId: townSq, Message: "later", FileIds: []string{f1, f2},
				Metadata: &model.PostMetadata{Files: []*model.FileInfo{{Id: f1, Name: "a.txt"}, {Id: f2, Name: "b.pdf"}}}}}
		fakemm.WriteJSON(w, 200, map[string][]*model.ScheduledPost{teamID: {sp}})
	})
	s.Handle(http.MethodPut, schedulePath+"/"+sched1, func(w http.ResponseWriter, r *http.Request) {
		var sp model.ScheduledPost
		_ = json.NewDecoder(r.Body).Decode(&sp)
		fakemm.WriteJSON(w, http.StatusOK, &sp)
	})
	for _, args := range [][]string{{"edit", sched1, "new text"}, {"reschedule", sched1, "tomorrow"}} {
		mustCode(t, run(app, append([]string{"scheduled"}, args...)...), 0)
		ids, meta, _ := scheduledFileIDs(t, s.Last(http.MethodPut, schedulePath+"/"+sched1).Body)
		if strings.Join(ids, ",") != f1+","+f2 || strings.Join(meta, ",") != f1+","+f2 {
			t.Fatalf("%s: file_ids %v, metadata.files %v", args[0], ids, meta)
		}
	}
}
