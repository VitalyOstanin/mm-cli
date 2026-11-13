package cli

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"net/url"
	"os/exec"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/godbus/dbus/v5"

	"github.com/VitalyOstanin/mm-cli/internal/clierr"
)

const (
	// notifyMaxTracked bounds the notifications whose link is kept for a
	// click; the oldest is forgotten first, so an ignored notification costs
	// one map entry until the bound pushes it out.
	notifyMaxTracked = 200
	// notifyOpenTimeout bounds the launch of the opener after a click.
	notifyOpenTimeout = 30 * time.Second
	// notifyBodyLength is how many characters of a message a notification
	// shows.
	notifyBodyLength = 100
	// notifyNowTimeout bounds a notification shown synchronously before the
	// process exits, so a stuck notifier never keeps watch from stopping.
	notifyNowTimeout = 5 * time.Second
	// toastAppID is the AppUserModelID of Windows PowerShell. A toast shown
	// under it needs no registration of an application of our own.
	toastAppID = `{1AC14E77-02E7-4E5D-B744-2EB1AE5198B7}\WindowsPowerShell\v1.0\powershell.exe`

	notifyHintMac = "mm-cli: install terminal-notifier (brew install terminal-notifier) to open the message from the notification"

	notifyDest = "org.freedesktop.Notifications"
	notifyPath = "/org/freedesktop/Notifications"
	// notifyCallTimeout bounds one D-Bus call to the notification service.
	notifyCallTimeout = 5 * time.Second
	notifyNoBus       = "--notify needs a desktop notification service on the session bus (is DBUS_SESSION_BUS_ADDRESS set?)"
	notifyNoOpener    = "mm-cli: a click will not open the message: xdg-open not found"
)

// notifyCmdTimeout bounds one background notifier command. A notifier can hang
// (terminal-notifier waiting for the user to allow notifications); without a
// bound every notification would add a stuck process until watch stops. It is
// a variable so that a test can shorten it.
var notifyCmdTimeout = 30 * time.Second

// notifyMaxProcs caps the notifier processes running at once on macOS and
// Windows. A burst of messages must not start an unbounded number of
// processes; when the cap is reached the notification is dropped (it is
// best effort, and waiting would block the watch loop for up to
// notifyCmdTimeout).
const notifyMaxProcs = 4

// Notification is a desktop notification about a watched event. AppLink is the
// mattermost:// link that opens the message in the Desktop app; empty for a
// notice that has nothing to open (no button, no click action). Image is the
// path of a picture to show with it (a custom emoji); empty for none.
type Notification struct {
	Title, Body, AppLink, Image string
}

// notifyCmd is the external command that shows a notification on macOS and
// Windows. It does not wait for a click: terminal-notifier and the Windows
// toast hand the link to the system, and osascript cannot open one.
type notifyCmd struct {
	name string
	args []string
}

// notifyCommand builds the command for goos (darwin or windows). It is a pure
// function so that the arguments, which carry message text, can be tested for
// every system; text only ever travels as separate arguments, never through a
// shell. group makes terminal-notifier keep notifications apart.
func notifyCommand(goos string, terminalNotifier bool, n Notification, group string) notifyCmd {
	if goos == "windows" {
		return notifyCmd{name: "powershell.exe", args: []string{
			"-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden",
			"-EncodedCommand", encodePowerShell(toastScript(n)),
		}}
	}
	if terminalNotifier {
		args := []string{"-title", tnValue(n.Title), "-message", tnValue(n.Body), "-group", group}
		if n.Image != "" {
			args = append(args, "-contentImage", n.Image)
		}
		if n.AppLink != "" {
			args = append(args, "-open", n.AppLink)
		}
		return notifyCmd{name: "terminal-notifier", args: args}
	}
	return notifyCmd{name: "osascript", args: []string{"-e", appleScript(n)}}
}

// tnValue protects a terminal-notifier option value: NSUserDefaults parses a
// value whose first character is [ ( { or " as a property list and the
// notification is then not sent. The README documents a leading backslash as
// the escape; only the first character needs it.
func tnValue(s string) string {
	if s != "" && strings.ContainsRune("[({\"", rune(s[0])) {
		return `\` + s
	}
	return s
}

// appleScript is the display notification statement for osascript.
func appleScript(n Notification) string {
	return "display notification " + appleQuote(n.Body) + " with title " + appleQuote(n.Title)
}

// appleQuote is s as an AppleScript string literal: backslash and double quote
// are the only characters that need escaping. Line breaks become spaces, as
// they would end the statement for -e.
func appleQuote(s string) string {
	s = strings.Join(strings.FieldsFunc(s, func(r rune) bool { return r == '\n' || r == '\r' }), " ")
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

// xmlEscape escapes the five characters that are special in XML text and
// attribute values.
func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;").Replace(s)
}

// toastScript is the PowerShell script that shows n as a toast. The XML-escaped
// values are embedded as base64 and decoded inside the script, so no character
// of a message can reach the script's syntax.
func toastScript(n Notification) string {
	b64 := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(xmlEscape(s))) }
	root := "<toast activationType=\"protocol\" launch=\"' + (D '" + b64(n.AppLink) + "') + '\">"
	if n.AppLink == "" {
		root = "<toast>"
	}
	image := ""
	if n.Image != "" {
		// The path travels as base64 like the text, so it cannot reach the
		// script's syntax either.
		image = "<image placement=\"appLogoOverride\" src=\"' + (D '" + b64(toastImageURL(n.Image)) + "') + '\"/>"
	}
	return strings.Join([]string{
		"$ErrorActionPreference = 'Stop'",
		"[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] | Out-Null",
		"[Windows.Data.Xml.Dom.XmlDocument, Windows.Data.Xml.Dom.XmlDocument, ContentType = WindowsRuntime] | Out-Null",
		"function D($s) { [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($s)) }",
		"$xml = New-Object Windows.Data.Xml.Dom.XmlDocument",
		"$xml.LoadXml('" + root + "<visual><binding template=\"ToastGeneric\"><text>' + (D '" +
			b64(n.Title) + "') + '</text><text>' + (D '" + b64(n.Body) + "') + '</text>" + image + "</binding></visual></toast>')",
		"$toast = [Windows.UI.Notifications.ToastNotification]::new($xml)",
		"[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier('" + toastAppID + "').Show($toast)",
	}, "\n")
}

// toastImageURL is the file:/// URL of a local path, as a toast wants it.
func toastImageURL(path string) string {
	return (&url.URL{Scheme: "file", Path: "/" + strings.ReplaceAll(path, `\`, "/")}).String()
}

// encodePowerShell is script as -EncodedCommand wants it: UTF-16LE in base64.
func encodePowerShell(script string) string {
	u := utf16.Encode([]rune(script))
	b := make([]byte, 0, len(u)*2)
	for _, c := range u {
		b = binary.LittleEndian.AppendUint16(b, c)
	}
	return base64.StdEncoding.EncodeToString(b)
}

// checkNotify fails before connecting on windows without powershell.exe.
// Other systems pass: the bus is checked by dialNotifyBus, and a missing
// terminal-notifier on macOS is handled in newDesktopNotifier.
func checkNotify(goos string, lookPath func(string) (string, error)) error {
	if goos == "windows" {
		if _, err := lookPath("powershell.exe"); err != nil {
			return clierr.New(clierr.CodeError, "--notify needs powershell.exe in PATH")
		}
	}
	return nil
}

// notifySignal is a click (action set) or the closing of notification id.
type notifySignal struct {
	id     uint32
	action bool
}

// urgency is the freedesktop urgency hint level. The spec defines it as a
// byte on the wire; the named type keeps bare 0/1/2 out of the callers.
type urgency byte

const (
	urgencyLow      urgency = 0
	urgencyNormal   urgency = 1
	urgencyCritical urgency = 2
)

// urgencyHint wraps the level for the Notify hints map. The spec wants a
// byte ("y"); a bare urgency value would be marshalled as an unknown type.
func urgencyHint(level urgency) dbus.Variant {
	return dbus.MakeVariant(byte(level))
}

// notifyBus is the part of the freedesktop notification service that watch
// uses. It is an interface so that tests fake the bus.
type notifyBus interface {
	// notify shows a notification and returns its id. level is the
	// freedesktop urgency hint.
	notify(ctx context.Context, title, body, image string, actions []string, level urgency) (uint32, error)
	// signals delivers clicks and closings until the bus is closed.
	signals() <-chan notifySignal
	close() error
}

// dialNotifyBus connects to the notification service; replaced in tests.
var dialNotifyBus = dialSessionNotifyBus

// dbusNotifier is notifyBus over the session bus. GNOME Shell sends
// ActionInvoked only to the connection that sent Notify, so one connection
// stays open for the whole run; notify-send cannot deliver a click for that
// reason once it has exited.
type dbusNotifier struct {
	conn *dbus.Conn
	sigs chan notifySignal
	done chan struct{}
}

func dialSessionNotifyBus() (notifyBus, error) {
	noBus := func(err error) error {
		return clierr.New(clierr.CodeError, "%s: %v", notifyNoBus, err)
	}
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, noBus(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), notifyCallTimeout)
	defer cancel()
	var name, vendor, version, spec string
	err = conn.Object(notifyDest, notifyPath).CallWithContext(ctx, notifyDest+".GetServerInformation", 0).
		Store(&name, &vendor, &version, &spec)
	if err != nil {
		_ = conn.Close()
		return nil, noBus(err)
	}
	if err := conn.AddMatchSignal(dbus.WithMatchObjectPath(notifyPath), dbus.WithMatchInterface(notifyDest)); err != nil {
		_ = conn.Close()
		return nil, noBus(err)
	}
	b := &dbusNotifier{conn: conn, sigs: make(chan notifySignal, 16), done: make(chan struct{})}
	raw := make(chan *dbus.Signal, 64)
	conn.Signal(raw)
	go forwardNotifySignals(raw, b.sigs, b.done)
	return b, nil
}

// forwardNotifySignals turns D-Bus signals into notifySignal until done is
// closed. It is the only sender on out and closes it on return, so that a
// reader ranging over the channel (desktopNotifier.listen) ends with the bus
// and Close does not wait for it forever.
func forwardNotifySignals(raw <-chan *dbus.Signal, out chan<- notifySignal, done <-chan struct{}) {
	defer close(out)
	for {
		select {
		case <-done:
			return
		case sg := <-raw:
			if sg == nil || len(sg.Body) == 0 {
				continue
			}
			id, ok := sg.Body[0].(uint32)
			if !ok {
				continue
			}
			var ns notifySignal
			switch sg.Name {
			case notifyDest + ".ActionInvoked":
				ns = notifySignal{id: id, action: true}
			case notifyDest + ".NotificationClosed":
				ns = notifySignal{id: id}
			default:
				continue
			}
			select {
			case out <- ns:
			case <-done:
				return
			}
		}
	}
}

func (b *dbusNotifier) notify(ctx context.Context, title, body, image string, actions []string, level urgency) (uint32, error) {
	ctx, cancel := context.WithTimeout(ctx, notifyCallTimeout)
	defer cancel()
	var id uint32
	hints := map[string]dbus.Variant{"urgency": urgencyHint(level)}
	if image != "" {
		// image-path is the freedesktop spec 1.2 hint; GNOME Shell shows it as the picture.
		hints["image-path"] = dbus.MakeVariant(image)
	}
	err := b.conn.Object(notifyDest, notifyPath).CallWithContext(ctx, notifyDest+".Notify", 0,
		"mm-cli", uint32(0), "", title, body, actions, hints, int32(-1)).Store(&id)
	return id, err
}

func (b *dbusNotifier) signals() <-chan notifySignal { return b.sigs }

func (b *dbusNotifier) close() error {
	close(b.done)
	return b.conn.Close()
}

// markupEscape escapes the characters GNOME reads as body markup.
func markupEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

// desktopNotifier shows notifications with the means of the running system:
// the D-Bus notification service on unix, commands on macOS and Windows.
type desktopNotifier struct {
	goos     string
	terminal bool
	// run runs a command and returns its output; ctx stops it.
	run func(ctx context.Context, name string, args ...string) (string, error)
	bus notifyBus // nil on macOS and Windows
	// urgency is the freedesktop urgency hint sent on the bus; macOS and
	// Windows have no equivalent and ignore it.
	urgency urgency

	mu    sync.Mutex
	links map[uint32]string
	order []uint32
	wg    sync.WaitGroup
	seq   int64
	// running counts the background notifier commands; guarded by mu.
	running int
}

func runOutput(ctx context.Context, name string, args ...string) (string, error) {
	// The names are fixed here (xdg-open, osascript, ...); the text is arguments.
	out, err := exec.CommandContext(ctx, name, args...).Output() //nolint:gosec // see above
	return string(out), err
}

func newDesktopNotifier(app *App, level urgency) (*desktopNotifier, error) {
	goos := app.goos()
	if err := checkNotify(goos, exec.LookPath); err != nil {
		return nil, err
	}
	d := &desktopNotifier{goos: goos, run: runOutput, links: map[uint32]string{}, urgency: level}
	switch goos {
	case "darwin":
		_, err := exec.LookPath("terminal-notifier")
		d.terminal = err == nil
		if !d.terminal {
			fmt.Fprintln(app.Err, notifyHintMac)
		}
	case "windows":
	default:
		bus, err := dialNotifyBus()
		if err != nil {
			return nil, err
		}
		if _, err := exec.LookPath("xdg-open"); err != nil {
			fmt.Fprintln(app.Err, notifyNoOpener)
		}
		d.bus = bus
		d.wg.Add(1)
		go d.listen(bus.signals())
	}
	return d, nil
}

// listen opens the link of a clicked notification and forgets the ones that
// were clicked or closed. It ends when the bus closes its channel or is closed.
func (d *desktopNotifier) listen(sigs <-chan notifySignal) {
	defer d.wg.Done()
	for sg := range sigs {
		link, ok := d.forget(sg.id)
		if !ok || !sg.action || link == "" {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), notifyOpenTimeout)
		_, _ = d.run(ctx, "xdg-open", link)
		cancel()
	}
}

func (d *desktopNotifier) forget(id uint32) (string, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	link, ok := d.links[id]
	delete(d.links, id)
	return link, ok
}

// track records the link of notification id; d.mu must be held.
func (d *desktopNotifier) track(id uint32, link string) {
	if _, ok := d.links[id]; !ok {
		d.order = append(d.order, id)
	}
	d.links[id] = link
	for len(d.order) > notifyMaxTracked {
		delete(d.links, d.order[0])
		d.order = d.order[1:]
	}
}

// Notify shows n and returns at once; on unix a click on it is handled by listen.
func (d *desktopNotifier) Notify(ctx context.Context, n Notification) {
	if d.bus != nil {
		var actions []string
		if n.AppLink != "" {
			actions = []string{"default", "Open"}
		}
		// The lock spans the call so that a click that arrives before the id
		// is recorded waits for it instead of being taken for an unknown id.
		d.mu.Lock()
		defer d.mu.Unlock()
		id, err := d.bus.notify(ctx, n.Title, markupEscape(n.Body), n.Image, actions, d.urgency)
		if err == nil && n.AppLink != "" {
			d.track(id, n.AppLink)
		}
		return
	}
	d.mu.Lock()
	if d.running >= notifyMaxProcs {
		d.mu.Unlock()
		return
	}
	d.running++
	group := d.nextGroupLocked()
	d.mu.Unlock()
	c := notifyCommand(d.goos, d.terminal, n, group)
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		defer func() {
			d.mu.Lock()
			d.running--
			d.mu.Unlock()
		}()
		rctx, cancel := context.WithTimeout(ctx, notifyCmdTimeout)
		defer cancel()
		_, _ = d.run(rctx, c.name, c.args...)
	}()
}

// nextGroupLocked returns a fresh terminal-notifier group name; the caller
// holds d.mu. Distinct groups keep concurrent notifications from replacing
// one another.
func (d *desktopNotifier) nextGroupLocked() string {
	d.seq++
	return fmt.Sprintf("mm-cli-%d", d.seq)
}

// NotifyNow shows n without an action and returns once it is up. It is for the
// last message before the process exits, so that the notification is not
// lost; it gives up after notifyNowTimeout.
func (d *desktopNotifier) NotifyNow(ctx context.Context, n Notification) {
	n.AppLink = ""
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), notifyNowTimeout)
	defer cancel()
	if d.bus != nil {
		_, _ = d.bus.notify(ctx, n.Title, markupEscape(n.Body), n.Image, nil, d.urgency)
		return
	}
	d.mu.Lock()
	group := d.nextGroupLocked()
	d.mu.Unlock()
	c := notifyCommand(d.goos, d.terminal, n, group)
	_, _ = d.run(ctx, c.name, c.args...)
}

// Close closes the bus and waits for the background work. A notification
// still on screen can no longer be clicked afterwards.
func (d *desktopNotifier) Close() {
	if d.bus != nil {
		_ = d.bus.close()
	}
	d.wg.Wait()
}
