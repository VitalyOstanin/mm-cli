package resolve

import (
	"strings"
	"testing"

	"github.com/VitalyOstanin/mm-cli/internal/clierr"
)

func FuzzParse(f *testing.F) {
	for _, s := range []string{
		"@me", "@alice", "@Alice", "town-square", "myteam:town-square",
		"abcdefghijklmnopqrstuvwxyz", "", "  ", "@", "@a b", "myteam:", ":town-square",
		"My Team:town-square", "myteam/town-square", "a/b/c", "a:b:c", "Town Square",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		a, err := Parse(in)
		if err != nil {
			if got := clierr.ExitCode(err); got != clierr.CodeUsage {
				t.Fatalf("Parse(%q) error %v has exit code %d, want usage", in, err, got)
			}
			return
		}
		if b, err2 := Parse(in); err2 != nil || b != a {
			t.Fatalf("Parse(%q) not deterministic: %+v vs %+v", in, a, b)
		}
		switch a.Kind {
		case KindSelf:
			if a != (Address{Kind: KindSelf}) || strings.TrimSpace(in) != "@me" {
				t.Fatalf("bad self address %+v for %q", a, in)
			}
		case KindDirect:
			if a.Username == "" || !usernameRe.MatchString(a.Username) || a.ID != "" || a.Team != "" || a.Name != "" {
				t.Fatalf("bad direct address %+v for %q", a, in)
			}
		case KindID:
			if !idRe.MatchString(a.ID) || a.Username != "" || a.Team != "" || a.Name != "" {
				t.Fatalf("bad id address %+v for %q", a, in)
			}
		case KindName:
			if a.Name == "" || !nameRe.MatchString(a.Name) || (a.Team != "" && !nameRe.MatchString(a.Team)) || a.ID != "" || a.Username != "" {
				t.Fatalf("bad name address %+v for %q", a, in)
			}
			if a.Team != "" {
				// ADR 0014: the slash form of a team address is rejected with a
				// hint that spells out the team:name replacement.
				want := a.Team + ":" + a.Name
				_, errSlash := Parse(a.Team + "/" + a.Name)
				if errSlash == nil || clierr.ExitCode(errSlash) != clierr.CodeUsage || !strings.Contains(errSlash.Error(), want) {
					t.Fatalf("Parse(%q) = %v, want usage error mentioning %q", a.Team+"/"+a.Name, errSlash, want)
				}
			}
		default:
			t.Fatalf("unknown kind %d for %q", a.Kind, in)
		}
	})
}
