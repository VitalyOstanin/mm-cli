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

func TestParseWhen(t *testing.T) {
	msk, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Skip("no time zone database")
	}
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
	s.JSON(http.MethodGet, "/api/v4/users/"+meID+"/channels", 200, []*model.Channel{
		{Id: townSq, Name: "town-square", Type: model.ChannelTypeOpen, TeamId: teamID},
		{Id: dmChan, Name: meID + "__" + bobID, Type: model.ChannelTypeDirect},
	})
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
	msk, _ := time.LoadLocation("Europe/Moscow")
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
	r = run(app, "send", "town-square", "later", "--reply", root, "--schedule", "2026-03-04T13:00:00Z", "--json")
	mustCode(t, r, 0)
	if s.Count(http.MethodGet, meteam) != before {
		t.Fatal("RFC 3339 looked up the profile time zone")
	}
	if got.RootId != root || got.RepeatType != "" || got.RepeatTimezone != "" {
		t.Fatalf("request: %+v", &got)
	}
	var out render.Scheduled
	if err := json.Unmarshal([]byte(r.out), &out); err != nil || out.ID != sched1 || out.Channel != "myteam/town-square" ||
		out.ScheduledAt != "2026-03-04T13:00:00Z" || out.Files == nil {
		t.Fatalf("json: %v %+v\n%s", err, out, r.out)
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
	cfg.Profiles["test"].Team = ""
	if err := app.Store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	r := run(app, "scheduled", "list")
	mustCode(t, r, 0)
	want := "2026-03-04 13:00  @bob  id " + sched3 + ", weekly, failed: user_deleted\nto bob\n\n" +
		"2026-03-04 14:00  #myteam/town-square  id " + sched1 + "\nlater\n\n" +
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
		out[0].Error != "user_deleted" || out[0].Repeat != "weekly" || out[0].Channel != "@bob" || out[1].Channel != "myteam/town-square" {
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
		t.Fatal("sent without --yes")
	}
	r := run(app, "scheduled", "send", sched1, "--yes", "--json")
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
	mustCode(t, run(app, "scheduled", "send", sched3, "--yes"), 2)
	if len(order) != 0 {
		t.Fatalf("requests: %v", order)
	}

	// When posting fails, the scheduled message stays.
	s.Handle(http.MethodPost, "/api/v4/posts", func(w http.ResponseWriter, _ *http.Request) {
		order = append(order, "create")
		fakemm.AppError(w, http.StatusForbidden, "no permission")
	})
	mustCode(t, run(app, "scheduled", "send", sched1, "--yes"), 4)
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
	r := run(app, "scheduled", "send", sched1, "--yes")
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
	mustCode(t, run(app, "scheduled", "delete", sched1, "bad", "--yes"), 2)
	if s.Count(http.MethodDelete, schedulePath+"/"+sched1) != 0 {
		t.Fatal("deleted before the checks")
	}
	r := run(app, "scheduled", "delete", sched1, sched2, sched3, "--yes", "--json")
	mustCode(t, r, 5)
	var out []actionResult
	if err := json.Unmarshal([]byte(r.out), &out); err != nil || len(out) != 1 || out[0].ID != sched1 || out[0].Action != "deleted" {
		t.Fatalf("json: %v %s", err, r.out)
	}
	if !strings.Contains(r.err, sched2) || s.Count(http.MethodDelete, schedulePath+"/"+sched3) != 0 {
		t.Fatalf("stderr %s", r.err)
	}
}
