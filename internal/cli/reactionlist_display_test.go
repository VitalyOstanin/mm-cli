package cli

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"

	"github.com/VitalyOstanin/mm-cli/internal/fakemm"
)

// listReactionsIn lists one reaction made on a message in ch, JSON and text.
func listReactionsIn(t *testing.T, ch *model.Channel) (js []map[string]any, text string) {
	t.Helper()
	app, s := newTestApp(t)
	p1 := fakemm.ID("p1")
	serveReactions(s, reactionPost(p1, ch.Id, time.Hour, reaction(p1, bobID, "thumbsup", 30*time.Minute)))
	s.JSON(http.MethodGet, "/api/v4/users/"+meID+"/channels", 200, []*model.Channel{ch})
	mustJSON(t, run(app, "post", "reaction", "list", "--json"), &js)
	r := run(app, "post", "reaction", "list")
	mustCode(t, r, 0)
	return js, r.out
}

func TestPostReactionListGroupChannelDisplay(t *testing.T) {
	grp := fakemm.ID("grp")
	js, text := listReactionsIn(t, &model.Channel{Id: grp, Name: "hash", DisplayName: "bob, carol", Type: model.ChannelTypeGroup})
	if len(js) != 1 || js[0]["channel"] != grp || js[0]["channel_display_name"] != "bob, carol" {
		t.Fatalf("json %v, want channel %s and channel_display_name \"bob, carol\"", js, grp)
	}
	if !strings.Contains(text, "  bob, carol\n") || strings.Contains(text, grp) {
		t.Errorf("text must show the display name, not the id:\n%s", text)
	}
}

func TestPostReactionListOpenChannelDisplay(t *testing.T) {
	js, text := listReactionsIn(t, &model.Channel{Id: townSq, TeamId: teamID, Name: "town-square", DisplayName: "Town Square", Type: model.ChannelTypeOpen})
	if len(js) != 1 || js[0]["channel"] != "town-square" || js[0]["channel_display_name"] != "Town Square" {
		t.Fatalf("json %v, want channel town-square and channel_display_name Town Square", js)
	}
	if !strings.Contains(text, "  town-square\n") || strings.Contains(text, "Town Square") {
		t.Errorf("text must show the address:\n%s", text)
	}
}
