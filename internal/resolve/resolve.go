// Package resolve turns channel addresses (@me, @user, name, team/name, id)
// into channel ids and caches the answers on disk.
package resolve

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"maps"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"

	"github.com/VitalyOstanin/mm-cli/internal/clierr"
	"github.com/VitalyOstanin/mm-cli/internal/config"
)

// Kind of address.
type Kind int

const (
	KindID Kind = iota
	KindSelf
	KindDirect
	KindName
)

// Address is a parsed channel address.
type Address struct {
	Kind     Kind
	ID       string
	Username string
	Team     string
	Name     string
}

var (
	usernameRe = regexp.MustCompile(`^[a-z0-9._-]+$`)
	// nameRe checks the team part of team/name; channel names are checked
	// by ValidChannelName.
	nameRe = regexp.MustCompile(`^[a-z0-9_-]+$`)
	idRe   = regexp.MustCompile(`^[a-z0-9]{26}$`)
)

// ValidChannelName reports whether s is a channel name (the last part of the
// channel URL) the server accepts: model.IsValidChannelIdentifier (a lowercase
// letter or digit, then lowercase letters, digits, '-' and '_') and at most
// model.ChannelNameMaxLength bytes. Both channel create and address parsing
// use it, so a name that can be created can also be addressed.
//
// A valid name of exactly 26 lowercase letters and digits also looks like a
// channel id; Parse reads such a bare string as an id, and team/name reaches
// the channel by name.
func ValidChannelName(s string) bool {
	return len(s) <= model.ChannelNameMaxLength && model.IsValidChannelIdentifier(s)
}

const addressHelp = "use @me, @user, channel-name, team/channel-name or a 26-character channel id"

// Parse parses a channel address.
func Parse(s string) (Address, error) {
	s = strings.TrimSpace(s)
	switch {
	case s == "":
		return Address{}, clierr.Usage("channel address is empty; %s", addressHelp)
	case s == "@me":
		return Address{Kind: KindSelf}, nil
	case strings.HasPrefix(s, "@"):
		u := strings.ToLower(s[1:])
		if !usernameRe.MatchString(u) {
			return Address{}, clierr.Usage("invalid user name in %q; %s", s, addressHelp)
		}
		return Address{Kind: KindDirect, Username: u}, nil
	case idRe.MatchString(s):
		return Address{Kind: KindID, ID: s}, nil
	}
	team, name := "", s
	if i := strings.IndexByte(s, '/'); i >= 0 {
		team, name = s[:i], s[i+1:]
		if !nameRe.MatchString(team) {
			return Address{}, clierr.Usage("invalid team name in %q; %s", s, addressHelp)
		}
	}
	if !ValidChannelName(name) {
		return Address{}, clierr.Usage("invalid channel name %q (use the name from the channel URL, not the display name); %s", s, addressHelp)
	}
	return Address{Kind: KindName, Team: team, Name: name}, nil
}

// NameTTL is how long a cached user name is trusted. An older name is still
// shown, but is checked with the server again (Cache.NameStale).
const NameTTL = 7 * 24 * time.Hour

// Cache maps addresses to ids. It is saved only when changed, and only the
// entries this run changed are written over the file, so that other runs of
// the same profile keep what they saved meanwhile.
type Cache struct {
	Channels map[string]string `json:"channels"`
	Users    map[string]string `json:"users"`
	Names    map[string]string `json:"names"`
	// NamesAt holds, per user id, when its name was fetched from the server,
	// in Unix seconds. A name without a time (a file written before times
	// were kept) is stale.
	NamesAt map[string]int64 `json:"names_at"`
	// Now is the clock for name fetch times; nil means time.Now.
	Now func() time.Time `json:"-"`
	// base and baseAt hold the entries as loaded or last saved, base in the
	// order of tables.
	base   [3]map[string]string
	baseAt map[string]int64
}

// NewCache returns an empty cache.
func NewCache() *Cache {
	return &Cache{
		Channels: map[string]string{}, Users: map[string]string{}, Names: map[string]string{},
		NamesAt: map[string]int64{},
	}
}

func (c *Cache) now() time.Time {
	if c.Now == nil {
		return time.Now()
	}
	return c.Now()
}

func (c *Cache) tables() [3]map[string]string {
	return [3]map[string]string{c.Channels, c.Users, c.Names}
}

// snapshot remembers the current entries as the unchanged state.
func (c *Cache) snapshot() {
	for i, m := range c.tables() {
		c.base[i] = maps.Clone(m)
	}
	c.baseAt = maps.Clone(c.NamesAt)
}

func (c *Cache) changed() bool {
	for i, m := range c.tables() {
		if !maps.Equal(m, c.base[i]) {
			return true
		}
	}
	return !maps.Equal(c.NamesAt, c.baseAt)
}

// LoadCache reads the cache; a missing or damaged file yields an empty one.
func LoadCache(path string) *Cache {
	c, _ := ReadCache(path)
	return c
}

// ReadCache is LoadCache that also returns why an existing file could not be
// used. A missing file is not an error. The cache is always usable.
func ReadCache(path string) (*Cache, error) {
	c := NewCache()
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(data, c); err != nil {
		return NewCache(), err
	}
	if c.Channels == nil {
		c.Channels = map[string]string{}
	}
	if c.Users == nil {
		c.Users = map[string]string{}
	}
	if c.Names == nil {
		c.Names = map[string]string{}
	}
	if c.NamesAt == nil {
		c.NamesAt = map[string]int64{}
	}
	c.snapshot()
	return c, nil
}

// Save writes the cache if it changed. It reads the file again and applies
// over it only the entries added, changed or removed since the cache was
// loaded, so entries saved meanwhile by another run of the profile are kept.
// Afterwards the cache holds the merged entries.
func (c *Cache) Save(path string) error {
	if !c.changed() {
		return nil
	}
	disk := LoadCache(path)
	dst := disk.tables()
	for i, cur := range c.tables() {
		mergeChanges(dst[i], c.base[i], cur)
	}
	mergeChanges(disk.NamesAt, c.baseAt, c.NamesAt)
	data, err := json.Marshal(disk)
	if err != nil {
		return err
	}
	if err := config.WriteFileAtomic(path, data, 0o600); err != nil {
		return err
	}
	c.Channels, c.Users, c.Names, c.NamesAt = disk.Channels, disk.Users, disk.Names, disk.NamesAt
	c.snapshot()
	return nil
}

// mergeChanges applies to dst the entries of cur added, changed or removed
// since base.
func mergeChanges[V comparable](dst, base, cur map[string]V) {
	for k, v := range cur {
		if old, ok := base[k]; !ok || old != v {
			dst[k] = v
		}
	}
	for k := range base {
		if _, ok := cur[k]; !ok {
			delete(dst, k)
		}
	}
}

// Name returns the cached username of a user id, stale or not.
func (c *Cache) Name(id string) (string, bool) {
	n, ok := c.Names[id]
	return n, ok
}

// NameStale reports whether the cached name of a user id was fetched more
// than NameTTL ago, or has no fetch time, and should be fetched again. An id
// without a cached name is not stale.
func (c *Cache) NameStale(id string) bool {
	if _, ok := c.Names[id]; !ok {
		return false
	}
	at, ok := c.NamesAt[id]
	return !ok || c.now().Sub(time.Unix(at, 0)) > NameTTL
}

// userFresh reports whether username maps to a user whose name is cached and
// not stale.
func (c *Cache) userFresh(username string) bool {
	id, ok := c.Users[username]
	return ok && c.Names[id] == username && !c.NameStale(id)
}

// forgetUser drops a username, the name of the user it led to and the
// direct channel under it.
func (c *Cache) forgetUser(username string) {
	if id, ok := c.Users[username]; ok && c.Names[id] == username {
		delete(c.Names, id)
		delete(c.NamesAt, id)
	}
	delete(c.Users, username)
	delete(c.Channels, "@"+username)
}

// PutUser records a user id and username fetched from the server now. A
// renamed user loses the old name, and a name taken over by another user stops
// leading to its previous owner, including the cached direct channel under
// that name.
func (c *Cache) PutUser(id, username string) {
	c.NamesAt[id] = c.now().Unix()
	if c.Names[id] == username && c.Users[username] == id {
		return
	}
	if old, ok := c.Names[id]; ok && old != username {
		if c.Users[old] == id {
			delete(c.Users, old)
		}
		delete(c.Channels, "@"+old)
	}
	if prev, ok := c.Users[username]; ok && prev != id {
		if c.Names[prev] == username {
			delete(c.Names, prev)
			delete(c.NamesAt, prev)
		}
		delete(c.Channels, "@"+username)
	}
	c.Names[id] = username
	c.Users[username] = id
}

func (c *Cache) putChannel(key, id string) {
	c.Channels[key] = id
}

// API is the part of Client4 the resolver needs.
type API interface {
	GetUserByUsername(ctx context.Context, userName, etag string) (*model.User, *model.Response, error)
	CreateDirectChannel(ctx context.Context, userId1, userId2 string) (*model.Channel, *model.Response, error)
	GetChannelByNameForTeamName(ctx context.Context, channelName, teamName string, etag string) (*model.Channel, *model.Response, error)
}

// Resolver resolves addresses for one user.
type Resolver struct {
	API   API
	Cache *Cache
	MeID  string
	Team  string
}

// Result is a resolved channel.
type Result struct {
	ChannelID string
	Cached    bool
	key       string
}

// Resolve returns the channel id of an address.
func (r *Resolver) Resolve(ctx context.Context, a Address) (Result, error) {
	if a.Kind == KindID {
		return Result{ChannelID: a.ID}, nil
	}
	var key string
	switch a.Kind {
	case KindSelf:
		key = "@me"
	case KindDirect:
		key = "@" + a.Username
		if err := r.recheckUser(ctx, a.Username); err != nil {
			return Result{}, err
		}
	case KindName:
		team := a.Team
		if team == "" {
			team = r.Team
		}
		if team == "" {
			return Result{}, clierr.Usage("no team selected for %q; pass --team or write team/%s", a.Name, a.Name)
		}
		a.Team = team
		key = team + "/" + a.Name
	}
	if id, ok := r.Cache.Channels[key]; ok {
		return Result{ChannelID: id, Cached: true, key: key}, nil
	}
	id, err := r.lookup(ctx, a)
	if err != nil {
		return Result{}, err
	}
	r.Cache.putChannel(key, id)
	return Result{ChannelID: id, key: key}, nil
}

func (r *Resolver) lookup(ctx context.Context, a Address) (string, error) {
	switch a.Kind {
	case KindSelf:
		return r.direct(ctx, r.MeID)
	case KindDirect:
		uid, ok := r.Cache.Users[a.Username]
		if !ok {
			u, resp, err := r.API.GetUserByUsername(ctx, a.Username, "")
			if err != nil {
				if resp != nil && resp.StatusCode == http.StatusNotFound {
					return "", clierr.Wrap(clierr.CodeNotFound, err, "user @%s not found", a.Username)
				}
				return "", clierr.API(resp, err)
			}
			uid = u.Id
			r.Cache.PutUser(u.Id, u.Username)
		}
		return r.direct(ctx, uid)
	default:
		ch, resp, err := r.API.GetChannelByNameForTeamName(ctx, a.Name, a.Team, "")
		if err != nil {
			if resp != nil && resp.StatusCode == http.StatusNotFound {
				return "", clierr.Wrap(clierr.CodeNotFound, err, "channel %q not found in team %q", a.Name, a.Team)
			}
			return "", clierr.API(resp, err)
		}
		return ch.Id, nil
	}
}

// recheckUser asks the server again about a username whose cached user is
// stale, so that a renamed user or a name taken over by someone else does not
// keep leading to the cached user and direct channel. A name the server no
// longer knows is forgotten and reported as not found; on another error the
// cached entries are kept and the error is returned, so a message never goes
// to a user that may no longer own the name. Names cached as nothing at all
// are left to lookup.
func (r *Resolver) recheckUser(ctx context.Context, username string) error {
	_, user := r.Cache.Users[username]
	_, ch := r.Cache.Channels["@"+username]
	if (!user && !ch) || r.Cache.userFresh(username) {
		return nil
	}
	u, resp, err := r.API.GetUserByUsername(ctx, username, "")
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			r.Cache.forgetUser(username)
			return clierr.Wrap(clierr.CodeNotFound, err, "user @%s not found", username)
		}
		return clierr.API(resp, err)
	}
	if r.Cache.Users[username] != u.Id {
		delete(r.Cache.Channels, "@"+username)
	}
	r.Cache.PutUser(u.Id, u.Username)
	return nil
}

func (r *Resolver) direct(ctx context.Context, other string) (string, error) {
	if r.MeID == "" {
		return "", clierr.New(clierr.CodeAuth, "the profile has no user id; run `mm-cli auth login`")
	}
	ch, resp, err := r.API.CreateDirectChannel(ctx, r.MeID, other)
	if err != nil {
		return "", clierr.API(resp, err)
	}
	return ch.Id, nil
}

// Forget drops a cached answer, e.g. after a 404 for the cached id.
func (r *Resolver) Forget(res Result) {
	if res.key == "" {
		return
	}
	delete(r.Cache.Channels, res.key)
}
