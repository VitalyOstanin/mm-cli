package render

import (
	"strings"
	"testing"
	"time"
)

func TestEmojiUnicode(t *testing.T) {
	for name, want := range map[string]string{
		"heart":                    "\u2764\ufe0f",
		"+1":                       "\U0001F44D",
		"eyes":                     "\U0001F440",
		"santa_light_skin_tone":    "\U0001F385\U0001F3FB",
		"skin-tone-3":              "\U0001F3FC",
		"partyparrot_not_standard": "",
		"":                         "",
	} {
		if got := EmojiUnicode(name); got != want {
			t.Errorf("EmojiUnicode(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestEmojiLabel(t *testing.T) {
	if got, want := EmojiLabel("+1"), "\U0001F44D :+1:"; got != want {
		t.Errorf("standard: %q, want %q", got, want)
	}
	if got, want := EmojiLabel("partyparrot_not_standard"), ":partyparrot_not_standard:"; got != want {
		t.Errorf("custom: %q, want %q", got, want)
	}
}

func TestReactionTextsShowEmojiCharacter(t *testing.T) {
	var b strings.Builder
	err := ReactionsText(&b, []Reaction{{User: "bob", Emoji: "eyes", Channel: "town-square", CreateAt: "2023-11-14T22:13:20Z"}}, time.UTC, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := b.String(); !strings.Contains(got, "@bob  \U0001F440 :eyes:  town-square") {
		t.Errorf("reaction block: %q", got)
	}
	b.Reset()
	err = PostsText(&b, []Post{{ID: "p", Reactions: []ReactionGroup{{Emoji: "+1", Count: 2}, {Emoji: "custom_one", Count: 1}}}}, time.UTC, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := b.String(); !strings.Contains(got, "  \U0001F44D :+1: 2  :custom_one: 1\n") {
		t.Errorf("counts line: %q", got)
	}
}

func TestReactionsTextMessageIsOneShortenedLine(t *testing.T) {
	var b strings.Builder
	rs := []Reaction{{User: "bob", Emoji: "eyes", Channel: "town-square", CreateAt: "2023-11-14T22:13:20Z", Message: "hello\n  big   world", AppLink: "mattermost://chat.example.com/myteam/pl/p1"}}
	if err := ReactionsText(&b, rs, time.UTC, 8); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
	if len(lines) != 3 || lines[1] != Snippet("hello big world", 8) || !strings.HasPrefix(lines[1], "hello") || strings.Contains(lines[1], "big world") {
		t.Fatalf("got %q", b.String())
	}
}
