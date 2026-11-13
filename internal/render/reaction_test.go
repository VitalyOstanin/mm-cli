package render

import "testing"

func TestPostLinks(t *testing.T) {
	for _, tc := range []struct{ url, perm, app string }{
		{"https://chat.example.com", "https://chat.example.com/myteam/pl/p1", "mattermost://chat.example.com/myteam/pl/p1"},
		{"https://chat.example.com/", "https://chat.example.com/myteam/pl/p1", "mattermost://chat.example.com/myteam/pl/p1"},
		{"http://chat.example.com:8065/mm", "http://chat.example.com:8065/mm/myteam/pl/p1", "mattermost://chat.example.com:8065/mm/myteam/pl/p1"},
		{"", "", ""},
	} {
		perm, app := PostLinks(tc.url, "myteam", "p1")
		if perm != tc.perm || app != tc.app {
			t.Errorf("PostLinks(%q) = %q, %q; want %q, %q", tc.url, perm, app, tc.perm, tc.app)
		}
	}
}

func TestPostLinksWithoutTeamUseRedirect(t *testing.T) {
	perm, app := PostLinks("https://chat.example.com", "", "p1")
	if perm != "https://chat.example.com/_redirect/pl/p1" || app != "mattermost://chat.example.com/_redirect/pl/p1" {
		t.Errorf("got %q, %q", perm, app)
	}
}
