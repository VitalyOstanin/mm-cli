package cli

import (
	"fmt"
	"regexp"
	"slices"
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
			if sub.Runnable() && sub.Annotations[groupAnnotation] == "" && strings.TrimSpace(sub.Example) == "" {
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

// TestHelpAgentsLists: the command lists of help agents match the commands.
// Every command says what it changes (effectAnnotation), so a new command
// without it fails here, and so does a list that misses one.
func TestHelpAgentsLists(t *testing.T) {
	app, _ := newTestApp(t)
	root := NewRootCommand(app)
	byEffect := map[string][]string{}
	var confirm []string
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			walk(sub)
			if !sub.Runnable() || sub.Annotations[groupAnnotation] != "" {
				continue
			}
			name := strings.TrimPrefix(sub.CommandPath(), "mm-cli ")
			e := sub.Annotations[effectAnnotation]
			switch e {
			case effectRead, effectServer, effectLocal, effectDesktop, effectLogin:
				byEffect[e] = append(byEffect[e], name)
			default:
				t.Errorf("%s: %s is %q, want one of read, server, local, desktop, login", name, effectAnnotation, e)
			}
			if sub.Flags().Lookup("confirm") != nil {
				confirm = append(confirm, name)
			}
		}
	}
	walk(root)

	text := strings.Join(strings.Fields(topicAgents), " ")
	m := regexp.MustCompile(`Commands that change state on the server: (.*?); (.*?) change only the local configuration, (.*?) only the desktop's link handlers\.`).FindStringSubmatch(text)
	if m == nil {
		t.Fatal("help agents: the list of commands that change state is not found")
	}
	c := regexp.MustCompile(`Commands that ask for confirmation on a terminal \((.*?)\)`).FindStringSubmatch(text)
	if c == nil {
		t.Fatal("help agents: the list of commands that ask for confirmation is not found")
	}
	for _, l := range []struct {
		what, inText string
		sep          string
		want         []string
	}{
		{"change state on the server", m[1], ", ", byEffect[effectServer]},
		{"change only the local configuration", m[2], " and ", byEffect[effectLocal]},
		{"change only the desktop's link handlers", m[3], " and ", byEffect[effectDesktop]},
		{"ask for confirmation", c[1], ", ", confirm},
	} {
		got := strings.Split(l.inText, l.sep)
		slices.Sort(got)
		want := slices.Sorted(slices.Values(l.want))
		if !slices.Equal(got, want) {
			t.Errorf("help agents, commands that %s:\n text     %v\n commands %v", l.what, got, want)
		}
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

// TestUnknownSubcommand: an unknown or removed command exits with code 2 at
// any depth, with --json too, and a group without arguments prints its help.
func TestUnknownSubcommand(t *testing.T) {
	app, _ := newTestApp(t)
	for _, args := range [][]string{
		{"pin", "add"}, {"channel", "read"}, {"thread", "read", "x"}, {"profile", "list"},
		{"profile", "picture", "remove"}, {"status", "custom", "get"}, {"channel", "read", "--json"},
		{"help", "nosuch"}, {"help", "channel", "read"},
	} {
		r := run(app, args...)
		mustCode(t, r, clierr.CodeUsage)
		if r.out != "" {
			t.Errorf("%v: stdout %q", args, r.out)
		}
	}
	r := run(app, "channel", "crate")
	mustCode(t, r, clierr.CodeUsage)
	if !strings.Contains(r.err, "did you mean create?") {
		t.Errorf("no suggestion: %s", r.err)
	}
	for _, args := range [][]string{{"channel"}, {"help", "channel"}, {"help", "agents"}, {"help"}} {
		r = run(app, args...)
		mustCode(t, r, 0)
		if !strings.Contains(r.out, "Usage:") && !strings.Contains(r.out, "agents") {
			t.Errorf("%v: no help:\n%s", args, r.out)
		}
	}
	r = run(app, "channel", "--help")
	if strings.Contains(r.out, "mm-cli channel [flags]") {
		t.Errorf("group usage has a line of its own:\n%s", r.out)
	}
}
