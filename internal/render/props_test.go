package render

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

const cardProps = `{"attachments":[{"id":0,"fallback":"Changes requested: feat: report (https://git.example.com/team/svc/-/merge_requests/127)",
 "color":"#bf8700","pretext":"","author_name":"","author_link":"","author_icon":"",
 "title":"!127 feat: report","title_link":"https://git.example.com/team/svc/-/merge_requests/127","text":"",
 "fields":[{"title":"Repository","value":"team/svc","short":true},{"title":"Findings","value":"Major 1","short":true},
           {"title":"Why","value":"1 major finding\nsee thread","short":false}],
 "image_url":"","thumb_url":"","footer":"reviewer · 3a072eee","footer_icon":"","ts":null}],
 "from_bot":"true","review_id":"9"}`

func postWithProps(t *testing.T, msg, props string) *model.Post {
	t.Helper()
	p := &model.Post{Id: "p1", UserId: "u1", Message: msg, CreateAt: base}
	if props != "" {
		var m map[string]any
		if err := json.Unmarshal([]byte(props), &m); err != nil {
			t.Fatal(err)
		}
		p.SetProps(m)
	}
	return p
}

func text(t *testing.T, p *model.Post, names map[string]string, truncate int) string {
	t.Helper()
	var buf bytes.Buffer
	if err := PostsText(&buf, []Post{FromModel(p, names)}, time.UTC, truncate); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestAttachmentText(t *testing.T) {
	p := postWithProps(t, "Changes requested · @bob", cardProps)
	got := text(t, p, map[string]string{"u1": "reviewer-bot"}, 0)
	want := `2026-03-04 10:30  @reviewer-bot  id p1, bot
Changes requested · @bob
  [attachment #bf8700]
    !127 feat: report <https://git.example.com/team/svc/-/merge_requests/127>
    Repository: team/svc
    Findings: Major 1
    Why: 1 major finding
      see thread
    reviewer · 3a072eee
`
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestAttachmentJSONKeepsProps(t *testing.T) {
	p := postWithProps(t, "x", cardProps)
	var buf bytes.Buffer
	if err := JSON(&buf, FromModel(p, nil)); err != nil {
		t.Fatal(err)
	}
	var out struct {
		Props map[string]any `json:"props"`
	}
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Props["from_bot"] != "true" || out.Props["review_id"] != "9" || len(out.Props["attachments"].([]any)) != 1 {
		t.Fatalf("props %v", out.Props)
	}
	if strings.Contains(buf.String(), `"type"`) || strings.Contains(buf.String(), `"embeds"`) || strings.Contains(buf.String(), `"priority"`) || strings.Contains(buf.String(), `"edit_at"`) {
		t.Fatalf("empty fields must be omitted: %s", buf.String())
	}
}

func TestPropsAreCopied(t *testing.T) {
	p := postWithProps(t, "x", `{"a":"1"}`)
	got := FromModel(p, nil)
	got.Props["a"] = "2"
	if p.GetProps()["a"] != "1" {
		t.Fatal("props aliased")
	}
}

func TestFallbackOnlyAttachment(t *testing.T) {
	p := postWithProps(t, "", `{"attachments":[{"fallback":"Build failed"}]}`)
	got := text(t, p, nil, 0)
	if !strings.HasSuffix(got, "  [attachment]\n    Build failed\n") {
		t.Fatalf("got %q", got)
	}
}

func TestActionsAndMedia(t *testing.T) {
	p := postWithProps(t, "", `{"attachments":[{"pretext":"Pick","author_name":"alice","author_link":"https://chat.example.com/alice",
		"text":"line1\nline2","image_url":"https://chat.example.com/i.png","thumb_url":"https://chat.example.com/t.png",
		"actions":[{"type":"button","name":"Approve"},{"type":"select","name":"Choose","options":[{"text":"opt1","value":"1"},{"text":"opt2","value":"2"}]}]}]}`)
	got := text(t, p, nil, 0)
	for _, w := range []string{
		"    Pick\n", "    alice <https://chat.example.com/alice>\n", "    line1\n    line2\n",
		"    image: https://chat.example.com/i.png\n", "    thumb: https://chat.example.com/t.png\n",
		"    [button] Approve\n", "    [select] Choose: opt1, opt2\n",
	} {
		if !strings.Contains(got, w) {
			t.Fatalf("missing %q in:\n%s", w, got)
		}
	}
}

func TestAttachmentTruncate(t *testing.T) {
	p := postWithProps(t, "", `{"attachments":[{"pretext":"abcdefghij","text":"klmnopqrst","fields":[{"title":"F","value":"uvwxyz0123"}]}]}`)
	got := text(t, p, nil, 3)
	for _, w := range []string{"    abc…\n", "    klm…\n", "    F: uvw…\n"} {
		if !strings.Contains(got, w) {
			t.Fatalf("missing %q in:\n%s", w, got)
		}
	}
}

func TestEmbeds(t *testing.T) {
	p := postWithProps(t, "see", "")
	p.Metadata = &model.PostMetadata{Embeds: []*model.PostEmbed{
		{Type: model.PostEmbedOpengraph, URL: "https://git.example.com/a", Data: map[string]any{"title": "Page", "site_name": "Docs"}},
		{Type: model.PostEmbedImage, URL: "https://chat.example.com/pic.png"},
		{Type: model.PostEmbedLink, URL: "https://chat.example.com/x"},
		{Type: model.PostEmbedPermalink, Data: map[string]any{"post": map[string]any{"user_id": "u2", "message": "quoted\ntext"}}},
		{Type: model.PostEmbedMessageAttachment},
		{Type: model.PostEmbedBoards},
	}}
	got := text(t, p, map[string]string{"u1": "alice", "u2": "bob"}, 0)
	want := "see\n  [link] Page — Docs: https://git.example.com/a\n  [image] https://chat.example.com/pic.png\n  [link] https://chat.example.com/x\n  [quote @bob] quoted text\n"
	if !strings.HasSuffix(got, want) {
		t.Fatalf("got:\n%s", got)
	}
	if ids := UserIDs(p); !slices.Contains(ids, "u2") {
		t.Fatalf("ids %v", ids)
	}
}

func TestPermalinkTypedPreview(t *testing.T) {
	p := postWithProps(t, "", "")
	p.Metadata = &model.PostMetadata{Embeds: []*model.PostEmbed{
		{Type: model.PostEmbedPermalink, Data: &model.PreviewPost{Post: &model.Post{UserId: "u3", Message: "typed"}}},
		{Type: model.PostEmbedPermalink, Data: 42},
	}}
	if ids := UserIDs(p); !slices.Contains(ids, "u3") {
		t.Fatalf("ids %v", ids)
	}
	got := text(t, p, map[string]string{"u3": "carol"}, 0)
	if !strings.Contains(got, "  [quote @carol] typed\n") {
		t.Fatalf("got %q", got)
	}
}

func TestPriorityAndAcknowledgements(t *testing.T) {
	p := postWithProps(t, "deploy now", "")
	urgent, yes := "urgent", true
	p.Metadata = &model.PostMetadata{
		Priority:         &model.PostPriority{Priority: &urgent, RequestedAck: &yes, PersistentNotifications: &yes},
		Acknowledgements: []*model.PostAcknowledgement{{UserId: "u2", AcknowledgedAt: base}, {UserId: "u3", AcknowledgedAt: base}},
	}
	names := map[string]string{"u1": "alice", "u2": "bob"}
	got := text(t, p, names, 0)
	want := "2026-03-04 10:30  @alice  id p1, urgent, acknowledgement requested, persistent notifications\ndeploy now\n  acknowledged: @bob, @u3\n"
	if got != want {
		t.Fatalf("got:\n%s", got)
	}
	fm := FromModel(p, names)
	if fm.Acknowledgements[0].User != "bob" || fm.Acknowledgements[1].User != "u3" || fm.Acknowledgements[0].AcknowledgedAt != "2026-03-04T10:30:00Z" {
		t.Fatalf("%+v", fm.Acknowledgements)
	}
	if ids := UserIDs(p); !slices.Contains(ids, "u2") || !slices.Contains(ids, "u3") {
		t.Fatalf("ids %v", ids)
	}
	empty := ""
	p.Metadata = &model.PostMetadata{Priority: &model.PostPriority{Priority: &empty}}
	if FromModel(p, nil).Priority != nil {
		t.Fatal("empty priority must be omitted")
	}
}

func TestHeaderFlags(t *testing.T) {
	p := postWithProps(t, "hi", `{"override_username":"Deploy Bot","from_webhook":"true"}`)
	p.Type = "custom_x"
	p.EditAt = base + 1
	got := text(t, p, map[string]string{"u1": "alice"}, 0)
	want := `2026-03-04 10:30  @alice as "Deploy Bot"  id p1, edited, webhook, type custom_x` + "\nhi\n"
	if got != want {
		t.Fatalf("got:\n%s", got)
	}
	if fm := FromModel(p, nil); fm.EditAt != "2026-03-04T10:30:00Z" || fm.Type != "custom_x" {
		t.Fatalf("%+v", fm)
	}
}

func TestInteractiveAndCard(t *testing.T) {
	p := postWithProps(t, "", `{"mm_blocks":[{"type":"x"}],"blocks":[],"card":"Title\nBody text"}`)
	p.Type = "card"
	got := text(t, p, nil, 0)
	want := "  [interactive mm_blocks] see \"props\" in --json\n  [card]\n    Title\n    Body text\n"
	if !strings.HasSuffix(got, want) {
		t.Fatalf("got %q", got)
	}
	if got := text(t, p, nil, 5); !strings.Contains(got, "    Title…\n") {
		t.Fatalf("card must be truncated: %q", got)
	}
}

func TestPlainText(t *testing.T) {
	cases := []struct {
		name, msg, props, want string
	}{
		{"message wins", "hello", `{"attachments":[{"title":"T"}]}`, "hello"},
		{"empty everything", "", "", ""},
		{"pretext first", "", `{"attachments":[{"pretext":"P","title":"T"}]}`, "P"},
		{"title", " ", `{"attachments":[{"title":"T","text":"X","fallback":"F"}]}`, "T"},
		{"fallback", "", `{"attachments":[{"fallback":"F"}]}`, "F"},
		{"card", "", `{"card":"C"}`, "C"},
		{"malformed", "", `{"attachments":"nope"}`, ""},
	}
	for _, c := range cases {
		if got := PlainText(postWithProps(t, c.msg, c.props)); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
	p := postWithProps(t, "", `{"attachments":[{"title":"Card title"}]}`)
	r := ReactionFromModel(&model.Reaction{UserId: "u1", EmojiName: "eyes"}, p, nil, "town-square", "", "", "myteam", "https://chat.example.com")
	if r.Message != "Card title" {
		t.Fatalf("reaction message %q", r.Message)
	}
}

func TestMalformedPropsDoNotPanic(t *testing.T) {
	for _, props := range []string{
		`{"attachments":"x"}`, `{"attachments":[1,null,"s",{"fields":"bad"}]}`,
		`{"attachments":[{"fields":[{"title":"n","value":5},{"title":"o","value":{"a":1}},{"title":"z","value":null},null],"actions":[null,{"type":"select"}]}]}`,
		`{"mm_blocks":"x","card":5,"from_bot":3,"override_username":7}`,
	} {
		p := postWithProps(t, "m", props)
		p.Metadata = &model.PostMetadata{Embeds: []*model.PostEmbed{nil, {Type: model.PostEmbedPermalink, Data: []int{1}}, {Type: model.PostEmbedOpengraph, Data: "str"}}}
		_ = text(t, p, nil, 2)
		_ = PlainText(p)
		_ = UserIDs(p)
	}
	got := text(t, postWithProps(t, "", `{"attachments":[{"fields":[{"title":"n","value":5}]}]}`), nil, 0)
	if !strings.Contains(got, "    n: 5\n") {
		t.Fatalf("got %q", got)
	}
}

func TestThreadsTextShowsAttachment(t *testing.T) {
	p := postWithProps(t, "", `{"attachments":[{"title":"Release"}]}`)
	th := Thread{ID: "t1", Label: "#design", ReplyCount: 1, Root: FromModel(p, map[string]string{"u1": "alice"})}
	var buf bytes.Buffer
	if err := ThreadsText(&buf, []Thread{th}, time.UTC, 0); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "  [attachment]\n    Release\n") {
		t.Fatalf("got %q", buf.String())
	}
}
