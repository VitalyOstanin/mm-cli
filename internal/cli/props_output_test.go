package cli

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"

	"github.com/VitalyOstanin/mm-cli/internal/fakemm"
	"github.com/VitalyOstanin/mm-cli/internal/render"
)

const cardURL = "https://git.example.com/team/svc/-/merge_requests/127"

func botCardPost() *model.Post {
	p := &model.Post{
		Id: fakemm.ID("card"), UserId: bobID, ChannelId: townSq, CreateAt: now.UnixMilli(),
		Message: "Changes requested",
		Metadata: &model.PostMetadata{Embeds: []*model.PostEmbed{
			{Type: model.PostEmbedMessageAttachment},
		}},
	}
	p.SetProps(model.StringInterface{
		"from_bot":           "true",
		"mr_reviewer_review": "9",
		"attachments": []any{map[string]any{
			"id": 0, "fallback": "Changes requested: feat: report", "color": "#bf8700",
			"title": "!127 feat: report", "title_link": cardURL,
			"fields": []any{
				map[string]any{"title": "Repository", "value": "team/svc", "short": true},
				map[string]any{"title": "Findings", "value": "Major 1", "short": true},
			},
			"footer": "reviewer · 3a072eee",
		}},
	})
	return p
}

func serveCard(s *fakemm.Server, p *model.Post) {
	s.JSON(http.MethodGet, "/api/v4/teams/name/myteam/channels/name/town-square", 200, &model.Channel{Id: townSq, Name: "town-square"})
	s.JSON(http.MethodGet, "/api/v4/channels/"+townSq+"/posts", 200, &model.PostList{Posts: map[string]*model.Post{p.Id: p}})
	s.JSON(http.MethodGet, "/api/v4/posts/"+p.Id, 200, p)
	s.JSON(http.MethodPost, "/api/v4/users/ids", 200, []*model.User{{Id: bobID, Username: "bob"}})
}

func checkCardText(t *testing.T, out string) {
	t.Helper()
	for _, want := range []string{
		", bot\n", "Changes requested", "[attachment #bf8700]",
		"!127 feat: report <" + cardURL + ">", "Repository: team/svc", "Findings: Major 1", "reviewer · 3a072eee",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func checkCardJSON(t *testing.T, got render.Post) {
	t.Helper()
	if got.Props["from_bot"] != "true" || got.Props["mr_reviewer_review"] != "9" {
		t.Errorf("props not verbatim: %v", got.Props)
	}
	atts, _ := got.Props["attachments"].([]any)
	if len(atts) != 1 {
		t.Fatalf("attachments: %v", got.Props["attachments"])
	}
	if a, _ := atts[0].(map[string]any); a["title_link"] != cardURL {
		t.Errorf("attachment: %v", atts[0])
	}
	if len(got.Embeds) != 1 || got.Embeds[0].Type != "message_attachment" {
		t.Errorf("embeds: %+v", got.Embeds)
	}
}

func TestMessagesShowBotCard(t *testing.T) {
	app, s := newTestApp(t)
	serveCard(s, botCardPost())
	r := run(app, "messages", "myteam:town-square")
	mustCode(t, r, 0)
	checkCardText(t, r.out)
	r = run(app, "messages", "myteam:town-square", "--json")
	mustCode(t, r, 0)
	var got []render.Post
	if err := json.Unmarshal([]byte(r.out), &got); err != nil || len(got) != 1 {
		t.Fatalf("%q %v", r.out, err)
	}
	checkCardJSON(t, got[0])
}

func TestPostGetShowsBotCard(t *testing.T) {
	app, s := newTestApp(t)
	p := botCardPost()
	serveCard(s, p)
	r := run(app, "post", "get", p.Id)
	mustCode(t, r, 0)
	checkCardText(t, r.out)
	r = run(app, "post", "get", p.Id, "--json")
	mustCode(t, r, 0)
	var got render.Post
	if err := json.Unmarshal([]byte(r.out), &got); err != nil {
		t.Fatalf("%q %v", r.out, err)
	}
	checkCardJSON(t, got)
}

func TestPostGetWithoutPropsOmitsFields(t *testing.T) {
	app, s := newTestApp(t)
	id := fakemm.ID("plain")
	s.JSON(http.MethodGet, "/api/v4/posts/"+id, 200, &model.Post{Id: id, UserId: bobID, Message: "hello", CreateAt: now.UnixMilli()})
	s.JSON(http.MethodPost, "/api/v4/users/ids", 200, []*model.User{{Id: bobID, Username: "bob"}})
	r := run(app, "post", "get", id, "--json")
	mustCode(t, r, 0)
	for _, key := range []string{`"props"`, `"embeds"`, `"priority"`, `"acknowledgements"`, `"type"`, `"edit_at"`} {
		if strings.Contains(r.out, key) {
			t.Errorf("%s must be omitted:\n%s", key, r.out)
		}
	}
}

func TestWatchedPostKeepsPostType(t *testing.T) {
	b, err := json.Marshal(watchedPost{Type: "post", PostType: "system_join_channel", Post: render.Post{ID: "x"}})
	if err != nil || !strings.Contains(string(b), `"type":"post"`) || !strings.Contains(string(b), `"post_type":"system_join_channel"`) {
		t.Fatalf("%s %v", b, err)
	}
}
