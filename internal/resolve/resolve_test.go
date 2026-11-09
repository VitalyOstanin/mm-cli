package resolve

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"

	"github.com/VitalyOstanin/mm-cli/internal/clierr"
)

const (
	meID  = "alice000000000000000000000"
	bobID = "bob00000000000000000000000"
)

type fakeAPI struct {
	direct, users, names int
}

func (f *fakeAPI) GetUserByUsername(_ context.Context, name, _ string) (*model.User, *model.Response, error) {
	f.users++
	if name != "bob" {
		return nil, &model.Response{StatusCode: 404}, &model.AppError{Message: "no user", StatusCode: 404}
	}
	return &model.User{Id: bobID, Username: "bob"}, &model.Response{StatusCode: 200}, nil
}

func (f *fakeAPI) CreateDirectChannel(_ context.Context, a, b string) (*model.Channel, *model.Response, error) {
	f.direct++
	return &model.Channel{Id: "dm-" + a[:3] + "-" + b[:3]}, &model.Response{StatusCode: 201}, nil
}

func (f *fakeAPI) GetChannelByNameForTeamName(_ context.Context, name, team, _ string) (*model.Channel, *model.Response, error) {
	f.names++
	if name != "town-square" {
		return nil, &model.Response{StatusCode: 404}, &model.AppError{Message: "no channel", StatusCode: 404}
	}
	return &model.Channel{Id: "ch-" + team}, &model.Response{StatusCode: 200}, nil
}

func TestParse(t *testing.T) {
	ok := map[string]Address{
		"@me":                        {Kind: KindSelf},
		"@Alice":                     {Kind: KindDirect, Username: "alice"},
		"town-square":                {Kind: KindName, Name: "town-square"},
		"myteam:town-square":         {Kind: KindName, Team: "myteam", Name: "town-square"},
		"abcdefghijklmnopqrstuvwxyz": {Kind: KindID, ID: "abcdefghijklmnopqrstuvwxyz"},
	}
	for in, want := range ok {
		got, err := Parse(in)
		if err != nil || got != want {
			t.Errorf("Parse(%q) = %+v, %v; want %+v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "a:b:c", ":x", "x:", "a/b/c", "Town Square", "@", "@a b"} {
		if _, err := Parse(bad); clierr.ExitCode(err) != clierr.CodeUsage {
			t.Errorf("Parse(%q) err = %v, want usage error", bad, err)
		}
	}
	// The old form team/channel is refused with the new form in the message.
	_, err := Parse("myteam/town-square")
	if clierr.ExitCode(err) != clierr.CodeUsage || !strings.Contains(err.Error(), "myteam:town-square") {
		t.Errorf("Parse(myteam/town-square) err = %v", err)
	}
}

func TestResolveSelfIsCached(t *testing.T) {
	api := &fakeAPI{}
	r := &Resolver{API: api, Cache: NewCache(), MeID: meID}
	a, _ := Parse("@me")
	res, err := r.Resolve(context.Background(), a)
	if err != nil || res.ChannelID != "dm-ali-ali" || res.Cached {
		t.Fatalf("%+v %v", res, err)
	}
	res, err = r.Resolve(context.Background(), a)
	if err != nil || !res.Cached || api.direct != 1 {
		t.Fatalf("second: %+v %v, calls %d", res, err, api.direct)
	}
	r.Forget(res)
	if _, err := r.Resolve(context.Background(), a); err != nil || api.direct != 2 {
		t.Fatalf("after Forget: %v, calls %d", err, api.direct)
	}
}

func TestResolveDirect(t *testing.T) {
	api := &fakeAPI{}
	c := NewCache()
	r := &Resolver{API: api, Cache: c, MeID: meID}
	a, _ := Parse("@bob")
	res, err := r.Resolve(context.Background(), a)
	if err != nil || res.ChannelID != "dm-ali-bob" {
		t.Fatalf("%+v %v", res, err)
	}
	if n, _ := c.Name(bobID); n != "bob" {
		t.Fatalf("name not cached: %v", c.Names)
	}
	a, _ = Parse("@carol")
	if _, err := r.Resolve(context.Background(), a); clierr.ExitCode(err) != clierr.CodeNotFound {
		t.Fatalf("unknown user: %v", err)
	}
}

func TestResolveName(t *testing.T) {
	api := &fakeAPI{}
	r := &Resolver{API: api, Cache: NewCache(), MeID: meID}
	a, _ := Parse("town-square")
	if _, err := r.Resolve(context.Background(), a); clierr.ExitCode(err) != clierr.CodeUsage {
		t.Fatalf("no team: %v", err)
	}
	r.Team = "myteam"
	res, err := r.Resolve(context.Background(), a)
	if err != nil || res.ChannelID != "ch-myteam" {
		t.Fatalf("%+v %v", res, err)
	}
	a, _ = Parse("other:town-square")
	if res, err := r.Resolve(context.Background(), a); err != nil || res.ChannelID != "ch-other" {
		t.Fatalf("explicit team: %+v %v", res, err)
	}
	a, _ = Parse("missing")
	if _, err := r.Resolve(context.Background(), a); clierr.ExitCode(err) != clierr.CodeNotFound {
		t.Fatalf("missing: %v", err)
	}
	a, _ = Parse("abcdefghijklmnopqrstuvwxyz")
	if res, _ := r.Resolve(context.Background(), a); res.ChannelID != "abcdefghijklmnopqrstuvwxyz" || api.names != 3 {
		t.Fatalf("id must not call the server: %+v, calls %d", res, api.names)
	}
}

func TestCacheFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "p", "resolve.json")
	c := LoadCache(p)
	if err := c.Save(p); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("unchanged cache must not be written")
	}
	c.PutUser(bobID, "bob")
	if err := c.Save(p); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		st, _ := os.Stat(p)
		if st.Mode().Perm() != 0o600 {
			t.Fatalf("mode %v", st.Mode().Perm())
		}
	}
	if n, _ := LoadCache(p).Name(bobID); n != "bob" {
		t.Fatal("round trip failed")
	}
	if err := os.WriteFile(p, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if c := LoadCache(p); len(c.Names) != 0 || c.Channels == nil {
		t.Fatalf("damaged file: %+v", c)
	}
}

func TestPutUserRename(t *testing.T) {
	c := NewCache()
	c.PutUser(bobID, "bob")
	c.Channels["@bob"] = "dm-bob"
	c.PutUser(bobID, "robert")
	if _, ok := c.Users["bob"]; ok {
		t.Errorf("old name still maps to an id: %v", c.Users)
	}
	if _, ok := c.Channels["@bob"]; ok {
		t.Errorf("old name still maps to a channel: %v", c.Channels)
	}
	if c.Users["robert"] != bobID || c.Names[bobID] != "robert" {
		t.Errorf("new name not recorded: %+v", c)
	}
}

func TestPutUserNameTakenByAnother(t *testing.T) {
	c := NewCache()
	c.PutUser(bobID, "bob")
	c.Channels["@bob"] = "dm-bob"
	c.PutUser(meID, "bob")
	if _, ok := c.Channels["@bob"]; ok {
		t.Errorf("@bob still leads to the previous owner of the name: %v", c.Channels)
	}
	if _, ok := c.Names[bobID]; ok {
		t.Errorf("the previous owner keeps the name: %v", c.Names)
	}
	if c.Users["bob"] != meID {
		t.Errorf("users: %v", c.Users)
	}
}

// Two runs for one server load the same file, change different entries and
// save one after the other: the second save must keep what the first wrote.
func TestCacheSaveMergesConcurrentChanges(t *testing.T) {
	p := filepath.Join(t.TempDir(), "resolve.json")
	seed := NewCache()
	seed.putChannel("myteam:old", "id-old")
	seed.putChannel("myteam:gone", "id-gone")
	if err := seed.Save(p); err != nil {
		t.Fatal(err)
	}
	a, b := LoadCache(p), LoadCache(p)
	a.PutUser(bobID, "bob")
	a.putChannel("myteam:old", "id-new")
	b.putChannel("myteam:town-square", "id-ts")
	(&Resolver{Cache: b}).Forget(Result{key: "myteam:gone"})
	if err := a.Save(p); err != nil {
		t.Fatal(err)
	}
	if err := b.Save(p); err != nil {
		t.Fatal(err)
	}
	got := LoadCache(p)
	if got.Users["bob"] != bobID || got.Names[bobID] != "bob" {
		t.Fatalf("first save lost: %+v", got)
	}
	if got.Channels["myteam:old"] != "id-new" || got.Channels["myteam:town-square"] != "id-ts" {
		t.Fatalf("channels: %+v", got.Channels)
	}
	if _, ok := got.Channels["myteam:gone"]; ok {
		t.Fatalf("forgotten entry came back: %+v", got.Channels)
	}
}

// A cache that was loaded but not changed is not written, even when the file
// changed meanwhile.
func TestCacheSaveUnchangedDoesNotWrite(t *testing.T) {
	p := filepath.Join(t.TempDir(), "resolve.json")
	c := LoadCache(p)
	other := LoadCache(p)
	other.PutUser(bobID, "bob")
	if err := other.Save(p); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if err := c.Save(p); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("unchanged cache must not be written")
	}
}

var t0 = time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)

// clock returns a cache clock that tests can move.
func clock(at *time.Time) func() time.Time { return func() time.Time { return *at } }

// A name fetched less than NameTTL ago is fresh; an older one is stale.
func TestNameStaleAfterTTL(t *testing.T) {
	at := t0
	c := NewCache()
	c.Now = clock(&at)
	c.PutUser(bobID, "bob")
	at = t0.Add(NameTTL - time.Second)
	if n, ok := c.Name(bobID); !ok || n != "bob" || c.NameStale(bobID) {
		t.Fatalf("young name: %q %v stale=%v", n, ok, c.NameStale(bobID))
	}
	at = t0.Add(NameTTL + time.Second)
	if n, ok := c.Name(bobID); !ok || n != "bob" || !c.NameStale(bobID) {
		t.Fatalf("old name: %q %v stale=%v", n, ok, c.NameStale(bobID))
	}
	c.PutUser(bobID, "bob")
	if c.NameStale(bobID) {
		t.Fatal("a name fetched again must be fresh")
	}
}

// A cache file written before names had fetch times is read, and all its
// names are stale.
func TestCacheFileWithoutNameTimesIsStale(t *testing.T) {
	p := filepath.Join(t.TempDir(), "resolve.json")
	old := `{"channels":{"@bob":"dm-bob"},"users":{"bob":"` + bobID + `"},"names":{"` + bobID + `":"bob"}}`
	if err := os.WriteFile(p, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := ReadCache(p)
	if err != nil {
		t.Fatal(err)
	}
	at := t0
	c.Now = clock(&at)
	if n, ok := c.Name(bobID); !ok || n != "bob" || !c.NameStale(bobID) {
		t.Fatalf("old entry: %q %v stale=%v", n, ok, c.NameStale(bobID))
	}
	c.PutUser(bobID, "bob")
	if err := c.Save(p); err != nil {
		t.Fatal(err)
	}
	got := LoadCache(p)
	got.Now = clock(&at)
	if got.NameStale(bobID) || got.Channels["@bob"] != "dm-bob" {
		t.Fatalf("refreshed entry not saved: %+v", got)
	}
}

// Refreshing the fetch time of a name is a change that Save writes and merges
// with what another run saved meanwhile.
func TestCacheSaveMergesNameTimes(t *testing.T) {
	p := filepath.Join(t.TempDir(), "resolve.json")
	at := t0
	seed := NewCache()
	seed.Now = clock(&at)
	seed.PutUser(bobID, "bob")
	if err := seed.Save(p); err != nil {
		t.Fatal(err)
	}
	at = t0.Add(NameTTL + time.Hour)
	a, b := LoadCache(p), LoadCache(p)
	a.Now, b.Now = clock(&at), clock(&at)
	a.PutUser(bobID, "bob")
	b.PutUser(meID, "alice")
	if err := a.Save(p); err != nil {
		t.Fatal(err)
	}
	if err := b.Save(p); err != nil {
		t.Fatal(err)
	}
	got := LoadCache(p)
	got.Now = clock(&at)
	if got.NameStale(bobID) || got.NameStale(meID) {
		t.Fatalf("fetch times lost: %+v", got)
	}
}

// @name with a fresh cached user uses the cache; with a stale one the user is
// checked again, so a name that now belongs to someone else leads to them.
func TestResolveDirectRechecksStaleUser(t *testing.T) {
	const carolID = "carol000000000000000000000"
	at := t0
	api := &fakeAPI{}
	c := NewCache()
	c.Now = clock(&at)
	c.PutUser(carolID, "bob")
	c.putChannel("@bob", "dm-carol")
	r := &Resolver{API: api, Cache: c, MeID: meID}
	a, _ := Parse("@bob")
	at = t0.Add(NameTTL - time.Second)
	if res, err := r.Resolve(context.Background(), a); err != nil || res.ChannelID != "dm-carol" || api.users != 0 {
		t.Fatalf("fresh: %+v %v, user lookups %d", res, err, api.users)
	}
	at = t0.Add(NameTTL + time.Second)
	res, err := r.Resolve(context.Background(), a)
	if err != nil || res.ChannelID != "dm-ali-bob" || api.users != 1 {
		t.Fatalf("stale: %+v %v, user lookups %d", res, err, api.users)
	}
	if c.Users["bob"] != bobID || c.NameStale(bobID) {
		t.Fatalf("user not refreshed: %+v", c)
	}
}

// A stale cached user whose name no longer exists is not found, and the
// cached direct channel under that name is dropped.
func TestResolveDirectStaleUserGone(t *testing.T) {
	const daveID = "dave000000000000000000000x"
	at := t0
	api := &fakeAPI{}
	c := NewCache()
	c.Now = clock(&at)
	c.PutUser(daveID, "dave")
	c.putChannel("@dave", "dm-dave")
	at = t0.Add(NameTTL + time.Second)
	r := &Resolver{API: api, Cache: c, MeID: meID}
	a, _ := Parse("@dave")
	if _, err := r.Resolve(context.Background(), a); clierr.ExitCode(err) != clierr.CodeNotFound {
		t.Fatalf("gone user: %v", err)
	}
	if _, ok := c.Channels["@dave"]; ok {
		t.Fatalf("channel kept: %v", c.Channels)
	}
	if _, ok := c.Users["dave"]; ok {
		t.Fatalf("user kept: %v", c.Users)
	}
}
