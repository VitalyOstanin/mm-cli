package cli

import (
	"net/http"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"

	"github.com/VitalyOstanin/mm-cli/internal/fakemm"
	"github.com/VitalyOstanin/mm-cli/internal/resolve"
)

// TestChannelListOutput pins the exact text and JSON output of channel list
// for type filters combined with --unread, duplicates and group channels.
func TestChannelListOutput(t *testing.T) {
	app, s := newTestApp(t)
	serveTownSquare(s)
	dm, gm, notes := fakemm.ID("dm"), fakemm.ID("gm"), fakemm.ID("notes")
	s.JSON(http.MethodGet, "/api/v4/users/"+meID+"/teams/"+teamID+"/channels", 200, []*model.Channel{
		{Id: townSq, Name: "town-square", DisplayName: "Town Square", Type: model.ChannelTypeOpen, TotalMsgCount: 10},
		{Id: notes, Name: "notes", DisplayName: "notes", Type: model.ChannelTypePrivate, TotalMsgCount: 3},
		{Id: dm, Name: meID + "__" + bobID, DisplayName: "dm", Type: model.ChannelTypeDirect, TotalMsgCount: 5},
		{Id: gm, Name: "group-name", DisplayName: "alice, bob", Type: model.ChannelTypeGroup, TotalMsgCount: 4},
		{Id: townSq, Name: "town-square", DisplayName: "Town Square", Type: model.ChannelTypeOpen, TotalMsgCount: 10},
	})
	s.JSON(http.MethodGet, "/api/v4/users/"+meID+"/teams/"+teamID+"/channels/members", 200, []model.ChannelMember{
		{ChannelId: townSq, MsgCount: 8, MentionCount: 1},
		{ChannelId: notes, MsgCount: 3, MentionCount: 2},
		{ChannelId: dm, MsgCount: 5},
		{ChannelId: gm, MsgCount: 1},
	})

	cases := []struct {
		args []string
		want string
	}{
		{[]string{"channel", "list"}, "private  notes  myteam/notes\n" +
			"open     Town Square  myteam/town-square\n" +
			"group    alice, bob  " + gm + "\n" +
			"direct   @bob  " + dm + "\n"},
		{[]string{"channel", "list", "--unread"}, "private  notes  myteam/notes  unread 0, mentions 2\n" +
			"open     Town Square  myteam/town-square  unread 2, mentions 1\n" +
			"group    alice, bob  " + gm + "  unread 3\n"},
		{[]string{"channel", "list", "--type", "d", "--unread"}, ""},
		{[]string{"channel", "list", "--type", "D", "--unread", "--json"}, "[]\n"},
		{[]string{"channel", "list", "--type", "O, G", "--unread", "--json"}, `[
  {
    "id": "` + townSq + `",
    "team": "myteam",
    "type": "open",
    "name": "town-square",
    "display_name": "Town Square",
    "messages": 10,
    "unread": 2,
    "mentions": 1
  },
  {
    "id": "` + gm + `",
    "type": "group",
    "name": "group-name",
    "display_name": "alice, bob",
    "messages": 4,
    "unread": 3
  }
]
`},
	}
	for _, c := range cases {
		r := run(app, c.args...)
		mustCode(t, r, 0)
		if r.out != c.want {
			t.Errorf("%v: got\n%s\nwant\n%s", c.args, r.out, c.want)
		}
	}
}

// TestChannelNameRuleMatchesAddress checks that channel create and the
// channel address parser accept and reject the same channel names.
func TestChannelNameRuleMatchesAddress(t *testing.T) {
	names := []string{
		"notes", "a", "town-square", "a_b", "0day", "notes-",
		"-notes", "_notes", "Notes", "no tes", "no.tes",
		strings.Repeat("a", 64), strings.Repeat("a", 65),
	}
	for _, name := range names {
		r := run(newEmptyApp(t), "channel", "create", "--", name)
		createRejects := strings.Contains(r.err, "invalid channel name")
		_, err := resolve.Parse(name)
		parseRejects := err != nil
		if createRejects != parseRejects {
			t.Errorf("%q: channel create rejects=%v, address parser rejects=%v (stderr: %s)", name, createRejects, parseRejects, r.err)
		}
	}
}
