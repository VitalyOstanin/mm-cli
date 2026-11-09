package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func FuzzValidateProfileName(f *testing.F) {
	for _, s := range []string{
		"default", "myteam", "alice-1", "a_b", "", "../x", "a/b", "a b", ".", "..",
		strings.Repeat("a", 64), strings.Repeat("a", 65), "имя", "a\x00b", "a\n",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, name string) {
		if ValidateProfileName(name) != nil {
			return
		}
		if len(name) < 1 || len(name) > 64 {
			t.Fatalf("accepted length %d: %q", len(name), name)
		}
		for _, r := range name {
			ok := r == '-' || r == '_' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
			if !ok {
				t.Fatalf("accepted %q with rune %q", name, r)
			}
		}
		if strings.ContainsAny(name, `/\`) || name == "." || name == ".." || filepath.Base(name) != name || filepath.Clean(name) != name {
			t.Fatalf("accepted unsafe file name %q", name)
		}
	})
}
