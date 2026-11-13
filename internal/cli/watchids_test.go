package cli

import (
	"net/http"
	"strings"
	"testing"

	"github.com/VitalyOstanin/mm-cli/internal/fakemm"
)

// Ids from events end up in API paths and in the link a click opens, so an
// event carrying a malformed id is dropped before any of that.
func TestWatchDropsEventsWithInvalidIDs(t *testing.T) {
	r := reactServer(t, func(town string) [][]byte {
		return [][]byte{
			reactionEvent(t, town, "../x", bobID, "badpost"),
			reactionEvent(t, town, "abc def", bobID, "badpost2"),
			reactionEvent(t, town, fakemm.ID("mine"), "bad user", "baduser"),
			postedBy(t, town, bobID, "@bob", "../y", "badpostid"),
			postedBy(t, "bad/chan", bobID, "@bob", fakemm.ID("p1"), "badchannel"),
			postedBy(t, town, "bad user", "@bob", fakemm.ID("p2"), "baduserpost"),
			reactionEvent(t, town, fakemm.ID("mine"), bobID, "sentinel"),
		}
	})
	out, _, stop := startWatch(t, r.app, "--messages", "--reactions")
	waitFor(t, "the sentinel", func() bool { return strings.Contains(out.String(), ":sentinel:") })
	stop()
	for _, bad := range []string{"badpost", "baduser", "badchannel", "baduserpost"} {
		if strings.Contains(out.String(), bad) {
			t.Errorf("event %q was printed:\n%s", bad, out.String())
		}
	}
	for _, p := range []string{"/api/v4/posts/..%2Fx", "/api/v4/posts/../x", "/api/v4/posts/abc%20def", "/api/v4/posts/abc def"} {
		if n := r.s.Count(http.MethodGet, p); n != 0 {
			t.Errorf("GET %s made %d times", p, n)
		}
	}
}
