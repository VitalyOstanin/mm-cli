package cli

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/VitalyOstanin/mm-cli/internal/render"
)

// TestWatchEventOutputForms pins the exact JSON and text forms of the two
// watch events, which share one printing helper.
func TestWatchEventOutputForms(t *testing.T) {
	w, _ := reactionWatcher(t)
	rec := render.Reaction{Channel: "town-square", Emoji: "tada", User: "bob", Message: "hi"}
	var buf bytes.Buffer
	w.s.app.Out = &buf

	w.s.app.json = true
	if err := w.emitReaction(context.Background(), rec, watchChannel{name: "town-square", display: "Town Square", reactionLabel: "town-square"}); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	if !strings.HasPrefix(got, `{"type":"reaction","post_id":`) || !strings.HasSuffix(got, "}\n") || strings.Count(got, "\n") != 1 {
		t.Errorf("json reaction = %q", got)
	}

	buf.Reset()
	w.s.app.json = false
	if err := w.emitReaction(context.Background(), rec, watchChannel{name: "town-square", display: "Town Square", reactionLabel: "town-square"}); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); !strings.HasPrefix(got, "[town-square] ") || !strings.HasSuffix(got, "\n\n") {
		t.Errorf("text reaction = %q", got)
	}

	buf.Reset()
	if err := w.printEvent(nil, "town-square", func(out io.Writer) error {
		_, err := io.WriteString(out, "body")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); got != "[town-square] body\n" {
		t.Errorf("text printEvent = %q", got)
	}
}
