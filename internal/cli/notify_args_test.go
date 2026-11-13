package cli

import "testing"

// terminal-notifier reads option values through NSUserDefaults, which parses a
// value starting with [ ( { or " as a property list; the documented escape is
// a leading backslash.
func TestNotifyCommandEscapesLeadingPlistChars(t *testing.T) {
	cases := []struct{ in, want string }{
		{"[x] done", `\[x] done`},
		{"(x)", `\(x)`},
		{"{x}", `\{x}`},
		{`"quoted"`, `\"quoted"`},
		{"-dash", "-dash"},
		{"plain [x]", "plain [x]"},
		{"", ""},
	}
	for _, c := range cases {
		cmd := notifyCommand("darwin", true, Notification{Title: c.in, Body: c.in}, "g")
		if cmd.args[1] != c.want || cmd.args[3] != c.want {
			t.Errorf("%q: title=%q body=%q, want %q", c.in, cmd.args[1], cmd.args[3], c.want)
		}
	}
}
