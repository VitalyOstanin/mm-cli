package cli

import "testing"

func TestSignInNoticeServerNameSamePath(t *testing.T) {
	if got := quoteServerName("test"); got != `"test"` {
		t.Errorf("quoteServerName = %s", got)
	}
	app, _ := newTestApp(t)
	if got := app.selectedServerName(); got != quoteServerName("test") {
		t.Errorf("selectedServerName = %s, want %s", got, quoteServerName("test"))
	}
	n := signInNotice(quoteServerName("my \"srv\""))
	want := `The session for "my \"srv\"" is missing or expired. Run: mm-cli auth login`
	if n.Body != want {
		t.Errorf("body = %q", n.Body)
	}
}
