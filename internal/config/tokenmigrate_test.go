package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/VitalyOstanin/mm-cli/internal/config"
)

// The token is in the store and verified; a file that cannot be removed is a
// warning, not a failed command.
func TestMigrateFileRemovalFailureWarns(t *testing.T) {
	s := testStore(t)
	if err := s.FileBackend().Set("work", "tok"); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(config.SessionPath(s, "work"))
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	if f, err := os.Create(filepath.Join(dir, "probe")); err == nil {
		_ = f.Close()
		t.Skip("directory permissions are not enforced here")
	}
	var warns []string
	s.OnWarn = func(server string, err error) { warns = append(warns, server+": "+err.Error()) }
	tok, err := s.LoadSession("work")
	if err != nil || tok != "tok" {
		t.Fatalf("got %q %v", tok, err)
	}
	if len(warns) != 1 {
		t.Fatalf("warnings: %v", warns)
	}
	if got, _ := s.Tokens.Get("work"); got != "tok" {
		t.Fatalf("store holds %q", got)
	}
}
