package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/VitalyOstanin/mm-cli/internal/clierr"
)

// handlerTestApp returns an App for goos whose external commands are recorded
// instead of run; fail names a command that fails.
func handlerTestApp(t *testing.T, goos, exe, fail string) (*App, *[]string, string) {
	t.Helper()
	app := newEmptyApp(t)
	dir := t.TempDir()
	app.GOOS = goos
	app.Env = func(k string) string {
		switch k {
		case "HOME":
			return dir
		case "XDG_DATA_HOME":
			return filepath.Join(dir, "data")
		}
		return ""
	}
	app.Executable = func() (string, error) { return exe, nil }
	var calls []string
	app.Run = func(name string, args ...string) error {
		calls = append(calls, name+" "+strings.Join(args, " "))
		if name == fail {
			return errors.New("failed")
		}
		return nil
	}
	return app, &calls, dir
}

func TestHandlerXDG(t *testing.T) {
	app, calls, dir := handlerTestApp(t, "linux", "/opt/mm cli/mm-cli", "")
	mustCode(t, run(app, "auth", "handler", "install"), 0)
	path := filepath.Join(dir, "data", "applications", "mm-cli-sso.desktop")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`Exec="/opt/mm cli/mm-cli" auth login --callback %u`, "MimeType=x-scheme-handler/mattermost-dev;"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("desktop entry lacks %q:\n%s", want, data)
		}
	}
	if len(*calls) == 0 || (*calls)[0] != "xdg-mime default mm-cli-sso.desktop x-scheme-handler/mattermost-dev" {
		t.Fatalf("calls %q", *calls)
	}
	r := run(app, "auth", "handler", "remove", "--json")
	mustCode(t, r, 0)
	if _, err := os.Stat(path); !os.IsNotExist(err) || !strings.Contains(r.out, `"action": "handler_removed"`) {
		t.Fatalf("not removed: %v %s", err, r.out)
	}
	mustCode(t, run(app, "auth", "handler", "remove"), 0)

	failing, _, _ := handlerTestApp(t, "linux", "/usr/bin/mm-cli", "xdg-mime")
	mustCode(t, run(failing, "auth", "handler", "install"), clierr.CodeError)
	quoted, _, _ := handlerTestApp(t, "linux", "/home/$x/mm-cli", "")
	mustCode(t, run(quoted, "auth", "handler", "install"), clierr.CodeUsage)
}

func TestHandlerWindows(t *testing.T) {
	app, calls, _ := handlerTestApp(t, "windows", `C:\Program Files\mm-cli\mm-cli.exe`, "")
	r := run(app, "auth", "handler", "install", "--json")
	mustCode(t, r, 0)
	want := []string{
		`reg add HKCU\Software\Classes\mattermost-dev /ve /d URL:mattermost-dev /f`,
		`reg add HKCU\Software\Classes\mattermost-dev /v URL Protocol /d  /f`,
		`reg add HKCU\Software\Classes\mattermost-dev\shell\open\command /ve /d "C:\Program Files\mm-cli\mm-cli.exe" auth login --callback "%1" /f`,
	}
	if strings.Join(*calls, "\n") != strings.Join(want, "\n") {
		t.Fatalf("calls:\n%s", strings.Join(*calls, "\n"))
	}
	if !strings.Contains(r.out, `"path": "HKCU\\Software\\Classes\\mattermost-dev"`) {
		t.Fatalf("json %s", r.out)
	}
	*calls = nil
	mustCode(t, run(app, "auth", "handler", "remove"), 0)
	if len(*calls) != 2 || !strings.HasPrefix((*calls)[1], `reg delete HKCU\Software\Classes\mattermost-dev /f`) {
		t.Fatalf("calls %q", *calls)
	}
}

func TestHandlerMac(t *testing.T) {
	app, calls, dir := handlerTestApp(t, "darwin", "/usr/local/bin/mm-cli", "")
	mustCode(t, run(app, "auth", "handler", "install"), 0)
	appDir := filepath.Join(dir, "Applications", "mm-cli SSO.app")
	plist := filepath.Join(appDir, "Contents", "Info.plist")
	got := strings.Join(*calls, "\n")
	for _, want := range []string{
		"osacompile -o " + appDir + " ",
		"plutil -replace CFBundleURLTypes -json " + `[{"CFBundleURLName":"mm-cli SSO login","CFBundleURLSchemes":["mattermost-dev"]}] ` + plist,
		"plutil -replace LSUIElement -bool true " + plist,
		"codesign --force --deep --sign - " + appDir,
		lsregister + " -f " + appDir,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q in calls:\n%s", want, got)
		}
	}
	if s := macHandlerScript("/usr/local/bin/mm-cli"); !strings.Contains(s, "on open location theURL") ||
		!strings.Contains(s, `quoted form of "/usr/local/bin/mm-cli" & " auth login --callback " & quoted form of theURL`) {
		t.Fatalf("script:\n%s", s)
	}
	// remove deletes the applet when it is there.
	if err := os.MkdirAll(filepath.Dir(plist), 0o755); err != nil {
		t.Fatal(err)
	}
	mustCode(t, run(app, "auth", "handler", "remove"), 0)
	if _, err := os.Stat(appDir); !os.IsNotExist(err) {
		t.Fatalf("applet left: %v", err)
	}
}
