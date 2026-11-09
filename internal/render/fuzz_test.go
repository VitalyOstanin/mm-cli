package render

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func FuzzShorten(f *testing.F) {
	for _, s := range []string{
		"hello world", "", "héllo", "👨\u200d👩\u200d👧 family", "🇷🇺🇺🇸🇩🇪", "éé", "a\u200db", "\xff\xfe", "👍🏽 ok",
	} {
		f.Add(s, 3)
		f.Add(s, 1)
		f.Add(s, 0)
		f.Add(s, -1)
		f.Add(s, 100)
	}
	f.Fuzz(func(t *testing.T, s string, n int) {
		got := shorten(s, n)
		if got == s {
			return
		}
		if n <= 0 {
			t.Fatalf("shorten(%q, %d) changed the text to %q", s, n, got)
		}
		if !strings.HasSuffix(got, "…") {
			t.Fatalf("shorten(%q, %d) = %q: cut without ellipsis", s, n, got)
		}
		head := strings.TrimSuffix(got, "…")
		if !strings.HasPrefix(s, head) || len(head) >= len(s) {
			t.Fatalf("shorten(%q, %d) = %q: head is not a proper prefix", s, n, got)
		}
		if head == "" {
			t.Fatalf("shorten(%q, %d) = %q: nothing kept", s, n, got)
		}
		if utf8.ValidString(s) {
			if !utf8.ValidString(got) {
				t.Fatalf("shorten(%q, %d) = %q: invalid UTF-8", s, n, got)
			}
			// A text with at most n runes has at most n clusters: no cut.
			// So a cut implies more than n runes.
			if utf8.RuneCountInString(s) <= n {
				t.Fatalf("shorten(%q, %d) cut a text of %d runes", s, n, utf8.RuneCountInString(s))
			}
			// Cut after n clusters: the head holds at least n runes.
			if utf8.RuneCountInString(head) < n {
				t.Fatalf("shorten(%q, %d) = %q: head shorter than n runes", s, n, got)
			}
		}
	})
}
