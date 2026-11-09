package cli

import (
	"fmt"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/VitalyOstanin/mm-cli/internal/clierr"
)

// helpTopicNames are the topics of mm-cli help <topic>.
var helpTopicNames = []string{"agents", "addressing", "output", "exit-codes", "login", "config", "load"}

// TestHelpComplete: every command an agent can run explains itself, with
// examples; every group says what it groups.
func TestHelpComplete(t *testing.T) {
	app, _ := newTestApp(t)
	root := NewRootCommand(app)
	topics := map[string]bool{}
	for _, n := range helpTopicNames {
		topics[n] = true
	}
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			if sub.Hidden || sub.Name() == "help" || sub.Name() == "completion" || topics[sub.Name()] {
				continue
			}
			path := sub.CommandPath()
			if strings.TrimSpace(sub.Long) == "" {
				t.Errorf("%s: no Long", path)
			}
			if sub.Runnable() && strings.TrimSpace(sub.Example) == "" {
				t.Errorf("%s: no Example", path)
			}
			walk(sub)
		}
	}
	walk(root)
	if strings.TrimSpace(root.Long) == "" {
		t.Error("mm-cli: no Long")
	}
}

// TestHelpTopics: every topic prints with exit code 0, and the root help
// points agents at them.
func TestHelpTopics(t *testing.T) {
	app, _ := newTestApp(t)
	for _, n := range helpTopicNames {
		r := run(app, "help", n)
		mustCode(t, r, 0)
		if len(strings.TrimSpace(r.out)) < 200 {
			t.Errorf("help %s is too short:\n%s", n, r.out)
		}
	}
	r := run(app, "--help")
	mustCode(t, r, 0)
	for _, w := range append([]string{"mm-cli help agents"}, helpTopicNames...) {
		if !strings.Contains(r.out, w) {
			t.Errorf("root help lacks %q", w)
		}
	}
}

// TestHelpExitCodes: the exit-codes topic names every code clierr has.
func TestHelpExitCodes(t *testing.T) {
	app, _ := newTestApp(t)
	r := run(app, "help", "exit-codes")
	mustCode(t, r, 0)
	for _, c := range []int{clierr.CodeOK, clierr.CodeError, clierr.CodeUsage, clierr.CodeAuth,
		clierr.CodeForbidden, clierr.CodeNotFound, clierr.CodeRateLimited, clierr.CodeNetwork,
		clierr.CodeInterrupted, clierr.CodeTerminated} {
		if !strings.Contains(r.out, fmt.Sprintf("\n  %-4d", c)) {
			t.Errorf("help exit-codes lacks code %d:\n%s", c, r.out)
		}
	}
}

// TestHelpPointsAtTopics: the help of any command ends with the pointer to
// the topics.
func TestHelpPointsAtTopics(t *testing.T) {
	app, _ := newTestApp(t)
	for _, args := range [][]string{{"post", "delete"}, {"channel"}, {"thread", "mark-read"}} {
		r := run(app, append(args, "--help")...)
		mustCode(t, r, 0)
		if !strings.Contains(r.out, helpPointer) {
			t.Errorf("%v --help lacks the pointer to the topics", args)
		}
	}
}
