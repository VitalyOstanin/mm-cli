package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/VitalyOstanin/mm-cli/internal/auth"
	"github.com/VitalyOstanin/mm-cli/internal/clierr"
)

const (
	// handlerDesktopFile is the desktop entry registered on Linux.
	handlerDesktopFile = "mm-cli-sso.desktop"
	// handlerApp is the applet registered on macOS, in ~/Applications.
	handlerApp = "mm-cli SSO.app"
	// handlerBundleID names the macOS applet.
	handlerBundleID = "io.github.vitalyostanin.mm-cli.sso"
	// handlerRegKey is the Windows registry key of the scheme.
	handlerRegKey = `HKCU\Software\Classes\` + auth.HandlerScheme
	lsregister    = "/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister"
)

// goos is the operating system the handler is registered for.
func (a *App) goos() string {
	if a.GOOS != "" {
		return a.GOOS
	}
	return runtime.GOOS
}

// runCmd runs an external command.
func (a *App) runCmd(name string, args ...string) error {
	if a.Run != nil {
		return a.Run(name, args...)
	}
	// A registration step takes seconds; a hung one should not hang mm-cli.
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	// The names are constants of this file (xdg-mime, reg, osacompile, ...).
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput() //nolint:gosec // see above
	if err != nil {
		return fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// executable is the absolute path of the running mm-cli.
func (a *App) executable() (string, error) {
	if a.Executable != nil {
		return a.Executable()
	}
	p, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(p)
}

func newAuthHandlerCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "handler",
		Short: "Let the browser finish an SSO login by itself",
		Long: `Register mm-cli as the handler of mattermost-dev:// links, so that an SSO
login (mm-cli auth login <url>) finishes without pasting the address.

After signing in, the /login/desktop page hands its address to a link: the
desktop app's mattermost:// normally, mattermost-dev:// for the logins mm-cli
starts. The browser asks whether to open it; mm-cli then finishes the login,
and the waiting auth login reports it. This also works when the browser is
already signed in to Mattermost and leaves the page at once.

Linux and other systems with freedesktop.org desktop entries, Windows and
macOS are supported. Desktop builds of the Mattermost app for development use
the same scheme; on a machine with one, install takes the scheme over.`,
	}
	cmd.AddCommand(newAuthHandlerInstallCmd(app), newAuthHandlerRemoveCmd(app))
	return cmd
}

func newAuthHandlerInstallCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:         "install",
		Annotations: effect(effectDesktop),
		Short:       "Register mm-cli for mattermost-dev:// links",
		Long: `Register this mm-cli binary as the handler of mattermost-dev:// links. The
registration names the binary by its path: run install again after moving it.

Linux: writes ~/.local/share/applications/` + handlerDesktopFile + ` (or under
$XDG_DATA_HOME) and runs xdg-mime default; update-desktop-database runs when
present. Windows: writes the registry key ` + handlerRegKey + `. macOS:
builds ~/Applications/` + handlerApp + ` with osacompile, adds the URL scheme
to its Info.plist, signs it ad hoc and registers it with Launch Services.

With --json it prints {"action": "handler_installed", "path": "<file, key or
app>"}. Exit code 2 when the binary's path cannot be written into the
registration (quotes or %, and on Linux and macOS also $, backquotes or
backslashes); 1 when a
system command fails.`,
		Example: `  mm-cli auth handler install
  mm-cli auth login https://chat.example.com`,
		Args: argsRange(0, 0),
		RunE: func(*cobra.Command, []string) error {
			exe, err := app.executable()
			if err != nil {
				return err
			}
			bad := "\"`$\\%\n" // desktop entry and AppleScript quoting
			if app.goos() == "windows" {
				bad = "\"%\n" // a Windows path has backslashes
			}
			if strings.ContainsAny(exe, bad) {
				return clierr.Usage("the path of mm-cli %q cannot be written into the registration; move the binary to a plain path", exe)
			}
			var path string
			switch app.goos() {
			case "windows":
				path, err = app.installWindowsHandler(exe)
			case "darwin":
				path, err = app.installMacHandler(exe)
			default:
				path, err = app.installXDGHandler(exe)
			}
			if err != nil {
				return err
			}
			return app.printDone(actionResult{Action: "handler_installed", Path: path},
				"Registered mm-cli for %s:// links: %s\nauth login now finishes when the browser opens the link.", auth.HandlerScheme, path)
		},
	}
}

func newAuthHandlerRemoveCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:         "remove",
		Annotations: effect(effectDesktop),
		Short:       "Remove the mattermost-dev:// registration",
		Long: `Remove what auth handler install wrote: the desktop entry on Linux, the
registry key on Windows, the applet on macOS. Removing a registration that is
not there is not an error. With --json it prints {"action":
"handler_removed", "path": ...}. Exit code 1 when a system command fails.`,
		Example: `  mm-cli auth handler remove
  mm-cli auth handler remove --json`,
		Args: argsRange(0, 0),
		RunE: func(*cobra.Command, []string) error {
			var path string
			var err error
			switch app.goos() {
			case "windows":
				path, err = app.removeWindowsHandler()
			case "darwin":
				path, err = app.removeMacHandler()
			default:
				path, err = app.removeXDGHandler()
			}
			if err != nil {
				return err
			}
			return app.printDone(actionResult{Action: "handler_removed", Path: path},
				"Removed the %s:// handler: %s", auth.HandlerScheme, path)
		},
	}
}

// home is the user's home directory.
func (a *App) home() (string, error) {
	if h := a.Env("HOME"); h != "" {
		return h, nil
	}
	return os.UserHomeDir()
}

// xdgApplications is the directory of the user's desktop entries.
func (a *App) xdgApplications() (string, error) {
	data := a.Env("XDG_DATA_HOME")
	if data == "" {
		h, err := a.home()
		if err != nil {
			return "", err
		}
		data = filepath.Join(h, ".local", "share")
	}
	return filepath.Join(data, "applications"), nil
}

func (a *App) installXDGHandler(exe string) (string, error) {
	dir, err := a.xdgApplications()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, handlerDesktopFile)
	entry := fmt.Sprintf(`[Desktop Entry]
Type=Application
Name=mm-cli SSO login
Comment=Finishes mm-cli auth login from the browser
Exec="%s" auth login --callback %%u
Terminal=false
NoDisplay=true
MimeType=x-scheme-handler/%s;
`, exe, auth.HandlerScheme)
	if err := os.WriteFile(path, []byte(entry), 0o644); err != nil { //nolint:gosec // a desktop entry is world-readable
		return "", err
	}
	if err := a.runCmd("xdg-mime", "default", handlerDesktopFile, "x-scheme-handler/"+auth.HandlerScheme); err != nil {
		return "", clierr.New(clierr.CodeError, "%s is written, but xdg-mime failed: %v", path, err)
	}
	_ = a.runCmd("update-desktop-database", dir) // optional: refreshes the MIME cache
	return path, nil
}

func (a *App) removeXDGHandler() (string, error) {
	dir, err := a.xdgApplications()
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, handlerDesktopFile)
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	_ = a.runCmd("update-desktop-database", dir)
	return path, nil
}

func (a *App) installWindowsHandler(exe string) (string, error) {
	steps := [][]string{
		{"add", handlerRegKey, "/ve", "/d", "URL:" + auth.HandlerScheme, "/f"},
		{"add", handlerRegKey, "/v", "URL Protocol", "/d", "", "/f"},
		{"add", handlerRegKey + `\shell\open\command`, "/ve", "/d", `"` + exe + `" auth login --callback "%1"`, "/f"},
	}
	for _, s := range steps {
		if err := a.runCmd("reg", s...); err != nil {
			return "", clierr.New(clierr.CodeError, "%v", err)
		}
	}
	return handlerRegKey, nil
}

func (a *App) removeWindowsHandler() (string, error) {
	if err := a.runCmd("reg", "query", handlerRegKey); err != nil {
		return handlerRegKey, nil // nothing to remove
	}
	if err := a.runCmd("reg", "delete", handlerRegKey, "/f"); err != nil {
		return "", clierr.New(clierr.CodeError, "%v", err)
	}
	return handlerRegKey, nil
}

// macApp is the path of the macOS applet.
func (a *App) macApp() (string, error) {
	h, err := a.home()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, "Applications", handlerApp), nil
}

// macHandlerScript is the AppleScript of the applet: macOS hands a URL to an
// application as an Apple Event, not as an argument, and "on open location"
// receives it.
func macHandlerScript(exe string) string {
	return fmt.Sprintf(`on open location theURL
	do shell script quoted form of "%s" & " auth login --callback " & quoted form of theURL
end open location
`, exe)
}

func (a *App) installMacHandler(exe string) (string, error) {
	app, err := a.macApp()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(app), 0o700); err != nil {
		return "", err
	}
	src, err := os.CreateTemp("", "mm-cli-sso-*.applescript")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.Remove(src.Name()) }()
	if _, err := src.WriteString(macHandlerScript(exe)); err != nil {
		_ = src.Close()
		return "", err
	}
	if err := src.Close(); err != nil {
		return "", err
	}
	if err := os.RemoveAll(app); err != nil {
		return "", err
	}
	plist := filepath.Join(app, "Contents", "Info.plist")
	urlTypes := fmt.Sprintf(`[{"CFBundleURLName":"mm-cli SSO login","CFBundleURLSchemes":["%s"]}]`, auth.HandlerScheme)
	steps := [][]string{
		{"osacompile", "-o", app, src.Name()},
		{"plutil", "-replace", "CFBundleIdentifier", "-string", handlerBundleID, plist},
		{"plutil", "-replace", "LSUIElement", "-bool", "true", plist},
		{"plutil", "-replace", "CFBundleURLTypes", "-json", urlTypes, plist},
		// The edited Info.plist no longer matches the applet's signature.
		{"codesign", "--force", "--deep", "--sign", "-", app},
		{lsregister, "-f", app},
	}
	for _, s := range steps {
		if err := a.runCmd(s[0], s[1:]...); err != nil {
			return "", clierr.New(clierr.CodeError, "%v", err)
		}
	}
	return app, nil
}

func (a *App) removeMacHandler() (string, error) {
	app, err := a.macApp()
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(app); errors.Is(err, fs.ErrNotExist) {
		return app, nil
	}
	_ = a.runCmd(lsregister, "-u", app)
	if err := os.RemoveAll(app); err != nil {
		return "", err
	}
	return app, nil
}
