package cli

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"

	"github.com/VitalyOstanin/mm-cli/internal/fakemm"
)

const userPath = "/api/v4/users/"

func serveAlice(s *fakemm.Server, props model.StringMap) {
	s.JSON(http.MethodGet, userPath+"me", 200, &model.User{Id: meID, Username: "alice", FirstName: "Alice", LastName: "Smith",
		Position: "Engineer", Email: "alice@example.com", Props: props,
		Timezone: model.StringMap{"useAutomaticTimezone": "false", "manualTimezone": "Europe/Berlin", "automaticTimezone": "UTC"}})
}

func TestProfileGet(t *testing.T) {
	app, s := newTestApp(t)
	serveAlice(s, nil)
	s.JSON(http.MethodGet, userPath+"username/bob", 200, &model.User{Id: bobID, Username: "bob", Nickname: "Bobby"})

	r := run(app, "profile", "get")
	mustCode(t, r, 0)
	for _, want := range []string{"username: @alice", "full name: Alice Smith", "position: Engineer", "timezone: Europe/Berlin\n"} {
		if !strings.Contains(r.out, want) {
			t.Fatalf("missing %q in\n%s", want, r.out)
		}
	}
	var p userProfile
	r = run(app, "profile", "get", "@bob", "--json")
	mustCode(t, r, 0)
	if err := json.Unmarshal([]byte(r.out), &p); err != nil || p.ID != bobID || p.Nickname != "Bobby" {
		t.Fatalf("json: %v %s", err, r.out)
	}
	mustCode(t, run(app, "profile", "get", "@carol"), 5)
}

func TestProfileEdit(t *testing.T) {
	app, s := newTestApp(t)
	serveAlice(s, nil)
	patchPath := userPath + meID + "/patch"
	var got model.UserPatch
	s.Handle(http.MethodPut, patchPath, func(w http.ResponseWriter, r *http.Request) {
		got = model.UserPatch{}
		_ = json.NewDecoder(r.Body).Decode(&got)
		fakemm.WriteJSON(w, 200, &model.User{Id: meID, Username: "alice", Position: "Lead"})
	})

	for _, args := range [][]string{
		{"profile", "edit"},
		{"profile", "edit", "--username", " "},
		{"profile", "edit", "--timezone", "Mars/Olympus"},
		{"profile", "edit", "--timezone", ""},
	} {
		mustCode(t, run(app, args...), 2)
	}
	if s.Count(http.MethodPut, patchPath) != 0 {
		t.Fatal("patched despite a usage error")
	}

	r := run(app, "profile", "edit", "--position", "Lead", "--nickname", "", "--json")
	mustCode(t, r, 0)
	if got.Position == nil || *got.Position != "Lead" || got.Nickname == nil || *got.Nickname != "" || got.FirstName != nil || got.Timezone != nil {
		t.Fatalf("patch: %+v", got)
	}

	mustCode(t, run(app, "profile", "edit", "--timezone", "Asia/Tokyo"), 0)
	if got.Timezone["manualTimezone"] != "Asia/Tokyo" || got.Timezone["useAutomaticTimezone"] != "false" || got.Timezone["automaticTimezone"] != "UTC" {
		t.Fatalf("timezone: %v", got.Timezone)
	}
	mustCode(t, run(app, "profile", "edit", "--timezone", "automatic"), 0)
	if got.Timezone["useAutomaticTimezone"] != "true" || got.Timezone["automaticTimezone"] != "UTC" || got.Timezone["manualTimezone"] != "Europe/Berlin" {
		t.Fatalf("timezone: %v", got.Timezone)
	}
}

func TestProfilePicture(t *testing.T) {
	app, s := newTestApp(t)
	img := filepath.Join(t.TempDir(), "photo.png")
	if err := os.WriteFile(img, []byte("\x89PNG fake"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.JSON(http.MethodPost, userPath+meID+"/image", 200, map[string]string{"status": "OK"})
	s.JSON(http.MethodDelete, userPath+meID+"/image", 200, map[string]string{"status": "OK"})

	mustCode(t, run(app, "profile", "picture", "set", filepath.Join(t.TempDir(), "missing.png")), 2)
	r := run(app, "profile", "picture", "set", img, "--json")
	mustCode(t, r, 0)
	if !strings.Contains(r.out, `"picture_set"`) || s.Count(http.MethodPost, userPath+meID+"/image") != 1 {
		t.Fatalf("output %q", r.out)
	}
	r = run(app, "profile", "picture", "reset")
	mustCode(t, r, 0)
	if r.out != "Profile picture reset.\n" {
		t.Fatalf("output %q", r.out)
	}
}

func TestStatusGet(t *testing.T) {
	app, s := newTestApp(t)
	cs := `{"emoji":"calendar","text":"In a meeting","duration":"one_hour","expires_at":"2026-03-04T13:00:00Z"}`
	serveAlice(s, model.StringMap{model.UserPropsKeyCustomStatus: cs})
	expired := `{"emoji":"house","text":"old","duration":"today","expires_at":"2026-03-03T23:59:59Z"}`
	s.JSON(http.MethodGet, userPath+"username/bob", 200, &model.User{Id: bobID, Username: "bob", Props: model.StringMap{model.UserPropsKeyCustomStatus: expired}})
	s.JSON(http.MethodPost, userPath+"status/ids", 200, []*model.Status{
		{UserId: meID, Status: model.StatusDnd, Manual: true, DNDEndTime: now.Add(2 * time.Hour).Unix()},
	})

	r := run(app, "status", "get", "@me", "@bob")
	mustCode(t, r, 0)
	want := "@alice  dnd until 2026-03-04 14:00\n  custom: :calendar: In a meeting (until 2026-03-04 13:00)\n@bob  offline\n"
	if r.out != want {
		t.Fatalf("got:\n%s\nwant:\n%s", r.out, want)
	}
	var out []userStatus
	r = run(app, "status", "get", "--json")
	mustCode(t, r, 0)
	if err := json.Unmarshal([]byte(r.out), &out); err != nil || len(out) != 1 || out[0].DNDEndAt != "2026-03-04T14:00:00Z" ||
		out[0].CustomStatus == nil || out[0].CustomStatus.Duration != "one_hour" || !out[0].Manual {
		t.Fatalf("json: %v %s", err, r.out)
	}
}

func TestStatusSet(t *testing.T) {
	app, s := newTestApp(t)
	statusPath := userPath + meID + "/status"
	var got model.Status
	s.Handle(http.MethodPut, statusPath, func(w http.ResponseWriter, r *http.Request) {
		got = model.Status{}
		_ = json.NewDecoder(r.Body).Decode(&got)
		got.Manual = true
		fakemm.WriteJSON(w, 200, &got)
	})

	for _, args := range [][]string{
		{"status", "set", "busy"},
		{"status", "set", "away", "--until", "1h"},
		{"status", "set", "dnd", "--until", "soon"},
		{"status", "set", "dnd", "--until", "-1h"},
	} {
		mustCode(t, run(app, args...), 2)
	}
	if s.Count(http.MethodPut, statusPath) != 0 {
		t.Fatal("status set despite a usage error")
	}

	r := run(app, "status", "set", "dnd", "--until", "90m", "--json")
	mustCode(t, r, 0)
	if got.Status != "dnd" || got.UserId != meID || got.DNDEndTime != now.Add(90*time.Minute).Unix() {
		t.Fatalf("request: %+v", &got)
	}
	var out []userStatus
	if err := json.Unmarshal([]byte(r.out), &out); err != nil || len(out) != 1 || out[0].User.Username != "alice" || out[0].DNDEndAt != "2026-03-04T13:30:00Z" {
		t.Fatalf("json: %v %s", err, r.out)
	}

	// tomorrow is 9:00 in the profile's time zone.
	serveAlice(s, nil)
	mustCode(t, run(app, "status", "set", "DND", "--until", "tomorrow"), 0)
	berlin, _ := time.LoadLocation("Europe/Berlin")
	if want := time.Date(2026, 3, 5, 9, 0, 0, 0, berlin).Unix(); got.DNDEndTime != want {
		t.Fatalf("dnd_end_time %d, want %d", got.DNDEndTime, want)
	}
	mustCode(t, run(app, "status", "set", "online"), 0)
	if got.Status != "online" || got.DNDEndTime != 0 {
		t.Fatalf("request: %+v", &got)
	}
}

func TestCustomStatus(t *testing.T) {
	app, s := newTestApp(t)
	serveAlice(s, nil)
	customPath := userPath + meID + "/status/custom"
	var got model.CustomStatus
	s.Handle(http.MethodPut, customPath, func(w http.ResponseWriter, r *http.Request) {
		got = model.CustomStatus{}
		_ = json.NewDecoder(r.Body).Decode(&got)
		fakemm.WriteJSON(w, 200, &got)
	})
	s.JSON(http.MethodDelete, customPath, 200, map[string]string{"status": "OK"})

	mustCode(t, run(app, "status", "custom", "set"), 2)
	mustCode(t, run(app, "status", "custom", "set", "x", "--clear-after", "later"), 2)
	if s.Count(http.MethodPut, customPath) != 0 {
		t.Fatal("set despite a usage error")
	}

	r := run(app, "status", "custom", "set", "Lunch")
	mustCode(t, r, 0)
	if got.Emoji != "speech_balloon" || got.Text != "Lunch" || got.Duration != "" || !got.ExpiresAt.IsZero() {
		t.Fatalf("request: %+v", got)
	}
	if r.out != "Custom status: :speech_balloon: Lunch\n" {
		t.Fatalf("output %q", r.out)
	}

	berlin, _ := time.LoadLocation("Europe/Berlin")
	tests := []struct {
		clear, duration string
		expires         time.Time
	}{
		{"1h", "one_hour", now.Add(time.Hour)},
		{"today", "today", time.Date(2026, 3, 4, 23, 59, 59, 0, berlin)},
		// 2026-03-04 is a Wednesday: the week ends on Sunday the 8th.
		{"this-week", "this_week", time.Date(2026, 3, 8, 23, 59, 59, 0, berlin)},
		{"2026-03-06 18:00", "date_and_time", time.Date(2026, 3, 6, 18, 0, 0, 0, berlin)},
	}
	for _, tc := range tests {
		r := run(app, "status", "custom", "set", "Away", "--emoji", ":palm_tree:", "--clear-after", tc.clear, "--json")
		mustCode(t, r, 0)
		if got.Emoji != "palm_tree" || got.Duration != tc.duration || !got.ExpiresAt.Equal(tc.expires) {
			t.Fatalf("%s: request %+v, want %s", tc.clear, got, tc.expires)
		}
		var out customStatusOut
		if err := json.Unmarshal([]byte(r.out), &out); err != nil || out.ExpiresAt != tc.expires.UTC().Format(time.RFC3339) {
			t.Fatalf("%s: json %v %s", tc.clear, err, r.out)
		}
	}

	r = run(app, "status", "custom", "clear", "--json")
	mustCode(t, r, 0)
	if !strings.Contains(r.out, `"custom_status_cleared"`) {
		t.Fatalf("output %q", r.out)
	}
}
