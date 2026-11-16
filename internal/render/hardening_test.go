package render

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

func renderPost(t *testing.T, p Post) string {
	t.Helper()
	var buf bytes.Buffer
	if err := PostsText(&buf, []Post{p}, time.UTC, 0); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// Every line after the header must be indented, so a value cannot forge
// a header of another message.
func assertNoForgedLine(t *testing.T, out string) {
	t.Helper()
	for i, l := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if i == 0 {
			continue
		}
		if !strings.HasPrefix(l, "  ") {
			t.Errorf("unindented line %q in:\n%s", l, out)
		}
	}
}

func TestSingleLineFieldsHaveNoNewlines(t *testing.T) {
	forged := "x\n2026-03-04 10:30  @admin  id p9\u2028y\u0085z\rw"
	a := &model.MessageAttachment{
		Title: forged, AuthorName: forged, Footer: forged, Color: forged,
		ImageURL: forged, ThumbURL: forged,
		Fields: []*model.MessageAttachmentField{{Title: forged, Value: "v"}},
		Actions: []*model.PostAction{
			{Name: forged, Type: model.PostActionTypeButton},
			{Name: forged, Type: model.PostActionTypeSelect, Options: []*model.PostActionOptions{{Text: forged, Value: "1"}}},
		},
	}
	p := Post{
		ID: "p1", CreateAt: "2026-03-04T10:30:00Z", User: User{Username: "bob"}, Type: "custom_" + forged,
		Props: map[string]any{
			model.PostPropsAttachments:      []*model.MessageAttachment{a},
			model.PostPropsOverrideUsername: forged,
		},
		Embeds: []Embed{
			{Type: string(model.PostEmbedOpengraph), URL: forged, Data: map[string]any{"title": forged, "site_name": forged, "url": forged}},
			{Type: string(model.PostEmbedImage), URL: forged},
			{Type: string(model.PostEmbedLink), URL: forged},
		},
	}
	out := renderPost(t, p)
	// header + attachment marker + embeds: no line may start unindented
	// except the header; the forged text must stay inside indented lines.
	assertNoForgedLine(t, out)
	if strings.ContainsAny(strings.Split(out, "\n")[0], "\r\u2028\u0085") {
		t.Errorf("header: %q", out)
	}
	for _, r := range []string{"\u2028", "\u2029", "\u0085", "\r"} {
		if strings.Contains(out, r) {
			t.Errorf("output contains %q:\n%s", r, out)
		}
	}
}

func TestFieldWithoutTitleStartingWithNewline(t *testing.T) {
	a := &model.MessageAttachment{Fields: []*model.MessageAttachmentField{
		{Title: "A", Value: "1"}, {Title: "", Value: "\nfoo"}, {Title: "B", Value: ""},
	}}
	var b strings.Builder
	writeAttachment(&b, a, 0)
	want := "  [attachment]\n    A: 1\n    foo\n    B:\n"
	if b.String() != want {
		t.Fatalf("got:\n%q\nwant:\n%q", b.String(), want)
	}
}

func TestQuoteWithoutUserID(t *testing.T) {
	var b strings.Builder
	writeEmbed(&b, Embed{Type: string(model.PostEmbedPermalink), Data: map[string]any{"post": map[string]any{"message": "x"}}}, nil)
	if want := "  [quote] x\n"; b.String() != want {
		t.Fatalf("got %q want %q", b.String(), want)
	}
}

func TestThreadsTextRootFlags(t *testing.T) {
	ack := true
	prio := model.PostPriorityUrgent
	root := FromModel(&model.Post{Id: "r1", UserId: "u1", Type: "custom_x", Message: "m",
		Props:    model.StringInterface{model.PostPropsFromBot: "true", model.PostPropsFromWebhook: "true"},
		Metadata: &model.PostMetadata{Priority: &model.PostPriority{Priority: &prio, RequestedAck: &ack}}}, map[string]string{"u1": "alice"})
	var b strings.Builder
	if err := ThreadsText(&b, []Thread{{ID: "r1", Label: "#town-square", Root: root}}, time.UTC, 0); err != nil {
		t.Fatal(err)
	}
	first := strings.SplitN(b.String(), "\n", 2)[0]
	for _, w := range []string{", bot", ", webhook", ", type custom_x", ", urgent", ", acknowledgement requested"} {
		if !strings.Contains(first, w) {
			t.Errorf("header %q lacks %q", first, w)
		}
	}
}

func TestSafeWriterReplacesBidi(t *testing.T) {
	in := "a\u202ab\u202ec\u2066d\u2069e\u200ef\u200fg\u061ch"
	var buf bytes.Buffer
	w := NewSafeWriter(&buf, nil)
	if _, err := w.Write([]byte(in)); err != nil {
		t.Fatal(err)
	}
	if want := "a\uFFFDb\uFFFDc\uFFFDd\uFFFDe\uFFFDf\uFFFDg\uFFFDh"; buf.String() != want {
		t.Fatalf("got %q want %q", buf.String(), want)
	}
}

func TestSafeWriterKeepsEmojiJoiners(t *testing.T) {
	in := "👨\u200d👩 ❤\ufe0f \u200c"
	var buf bytes.Buffer
	if _, err := NewSafeWriter(&buf, nil).Write([]byte(in)); err != nil {
		t.Fatal(err)
	}
	if buf.String() != in {
		t.Fatalf("got %q", buf.String())
	}
}
