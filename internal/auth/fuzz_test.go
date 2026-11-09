package auth

import (
	"net/url"
	"regexp"
	"strings"
	"testing"
)

func FuzzParseCallback(f *testing.F) {
	const srv = "https://chat.example.com"
	for _, s := range []string{
		" https://chat.example.com/login/desktop?client_token=ct&server_token=st \n",
		"https://chat.example.com/login/desktop/?client_token=ct&server_token=st",
		"https://evil.example.net/login/desktop?client_token=ct&server_token=st",
		"http://chat.example.com/login/desktop?client_token=ct&server_token=st",
		"curl 'http://chat.example.com/login/desktop?client_token=ct&server_token=st' \\\n  -H 'DNT: 1'",
		"https://alice@chat.example.com/login/desktop?client_token=ct&server_token=st",
		"https://CHAT.example.com:443/login/desktop?client_token=ct&server_token=st",
		"https://chat.example.com/login?client_token=ct&server_token=st",
		"https://chat.example.com/login/desktop?client_token=xx&server_token=st",
		"https://chat.example.com/login/desktop?client_token=ct",
		"hello",
		"",
	} {
		f.Add(s)
	}
	re := regexp.MustCompile(`https?://[^\s'"<>]*/login/desktop[^\s'"<>]*`)
	f.Fuzz(func(t *testing.T, pasted string) {
		st, err := ParseCallback(srv, pasted, "ct")
		if err != nil {
			if st != "" {
				t.Fatalf("token %q returned together with error %v", st, err)
			}
			return
		}
		if st == "" {
			t.Fatalf("empty server_token without error for %q", pasted)
		}
		// Recompute the address the function must have used.
		text := strings.TrimSpace(pasted)
		if m := re.FindString(text); m != "" {
			text = m
		}
		u, perr := url.Parse(text)
		if perr != nil {
			t.Fatalf("accepted an address that does not parse: %v", perr)
		}
		if !strings.EqualFold(u.Host, "chat.example.com") {
			t.Fatalf("accepted foreign host %q from %q", u.Host, pasted)
		}
		if !strings.HasSuffix(strings.TrimRight(u.Path, "/"), "/login/desktop") {
			t.Fatalf("accepted path %q", u.Path)
		}
		q := u.Query()
		if q.Get("client_token") != "ct" {
			t.Fatalf("accepted client_token %q", q.Get("client_token"))
		}
		if q.Get("server_token") != st {
			t.Fatalf("returned %q, URL carries %q", st, q.Get("server_token"))
		}
		// A different expected client token must always be refused.
		if _, err := ParseCallback(srv, pasted, "ct-other"); err == nil {
			t.Fatalf("accepted a mismatching client token for %q", pasted)
		}
	})
}
