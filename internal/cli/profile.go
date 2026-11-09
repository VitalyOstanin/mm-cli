package cli

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/spf13/cobra"

	"github.com/VitalyOstanin/mm-cli/internal/clierr"
)

// userProfile is the --json output of profile get and profile edit. Field
// names are a public interface.
type userProfile struct {
	ID        string `json:"id"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Nickname  string `json:"nickname"`
	Position  string `json:"position"`
	Email     string `json:"email"`
	// Timezone is the IANA name in effect: the automatic one or the manual
	// one, as TimezoneAutomatic says; "" when none is set.
	Timezone          string `json:"timezone"`
	TimezoneAutomatic bool   `json:"timezone_automatic"`
}

func toProfile(u *model.User) userProfile {
	return userProfile{
		ID: u.Id, Username: u.Username, FirstName: u.FirstName, LastName: u.LastName,
		Nickname: u.Nickname, Position: u.Position, Email: u.Email,
		Timezone:          model.GetPreferredTimezone(u.Timezone),
		TimezoneAutomatic: u.Timezone["useAutomaticTimezone"] == "true",
	}
}

func (a *App) printProfile(p userProfile) error {
	if a.json {
		return a.printJSON(p)
	}
	tz := p.Timezone
	if p.TimezoneAutomatic {
		tz += " (automatic)"
	}
	for _, f := range [][2]string{
		{"username", "@" + p.Username}, {"full name", strings.TrimSpace(p.FirstName + " " + p.LastName)},
		{"nickname", p.Nickname}, {"position", p.Position}, {"email", p.Email}, {"timezone", tz}, {"id", p.ID},
	} {
		fmt.Fprintf(a.Out, "%s: %s\n", f[0], f[1])
	}
	return nil
}

// userArg returns the user named by @me, @username or username.
func userArg(ctx context.Context, s *Session, arg string) (*model.User, error) {
	name := strings.TrimPrefix(arg, "@")
	if name == "me" {
		u, resp, err := s.Client.GetMe(ctx, "")
		if err != nil {
			return nil, clierr.API(resp, err)
		}
		return u, nil
	}
	if name == "" {
		return nil, clierr.Usage("empty user name")
	}
	u, resp, err := s.Client.GetUserByUsername(ctx, name, "")
	if err != nil {
		return nil, clierr.API(resp, err)
	}
	return u, nil
}

// userArgs returns the users named by @me, @username or username, in the
// order given and each once, with one request for all the usernames.
func userArgs(ctx context.Context, s *Session, args []string) ([]*model.User, error) {
	var names []string
	wantMe := false
	for _, a := range args {
		switch name := strings.TrimPrefix(a, "@"); name {
		case "":
			return nil, clierr.Usage("empty user name")
		case "me":
			wantMe = true
		default:
			if !slices.Contains(names, name) {
				names = append(names, name)
			}
		}
	}
	byName := map[string]*model.User{}
	if wantMe {
		me, resp, err := s.Client.GetMe(ctx, "")
		if err != nil {
			return nil, clierr.API(resp, err)
		}
		byName["me"] = me
	}
	if len(names) > 0 {
		found, resp, err := s.Client.GetUsersByUsernames(ctx, names)
		if err != nil {
			return nil, clierr.API(resp, err)
		}
		for _, u := range found {
			byName[u.Username] = u
		}
	}
	var out []*model.User
	seen := map[string]bool{}
	for _, a := range args {
		u := byName[strings.TrimPrefix(a, "@")]
		if u == nil {
			return nil, clierr.New(clierr.CodeNotFound, "user %s not found", a)
		}
		if !seen[u.Id] {
			seen[u.Id] = true
			out = append(out, u)
		}
	}
	return out, nil
}

func newProfileCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "profile",
		Short: "Your profile on the server: names, position, time zone, picture",
		Long: `Your user profile on the Mattermost server, as in Profile Settings and
Display > Timezone of the app: username, full name (first and last name),
nickname, position, email, time zone and profile picture. Other users'
profiles can be read. Saved server connections are under mm-cli auth.

A profile in --json output is {"id", "username", "first_name", "last_name",
"nickname", "position", "email", "timezone" (the IANA name in effect, or ""),
"timezone_automatic" (true when it follows the machine)}. The server may
leave "email" empty for other users, depending on its privacy settings.`,
		Example: `  mm-cli profile get
  mm-cli profile edit --position "Release manager"
  mm-cli profile picture set photo.png`,
	}
	cmd.AddCommand(newProfileGetCmd(app), newProfileEditCmd(app), newProfilePictureCmd(app))
	return cmd
}

func newProfileGetCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:         "get [@user]",
		Annotations: effect(effectRead),
		Short:       "Show your profile or another user's",
		Long: `Show a user profile: yours without an argument (or with @me), another user's
with @username.

With --json it prints a profile (mm-cli profile --help). Exit code 5 for an
unknown user.`,
		Example: `  mm-cli profile get
  mm-cli profile get @bob --json`,
		Args: argsRange(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			who := "@me"
			if len(args) == 1 {
				who = args[0]
			}
			s, err := app.openSession()
			if err != nil {
				return err
			}
			defer s.Close()
			u, err := userArg(cmd.Context(), s, who)
			if err != nil {
				return err
			}
			return app.printProfile(toProfile(u))
		},
	}
}

// automaticTimezone is the value of profile edit --timezone that makes the
// time zone follow the machine.
const automaticTimezone = "automatic"

// profileFields are the text fields of profile edit: the flag, its help and
// the field of the patch it sets.
var profileFields = []struct {
	flag, help string
	field      func(*model.UserPatch) **string
}{
	{"first-name", "first name", func(p *model.UserPatch) **string { return &p.FirstName }},
	{"last-name", "last name", func(p *model.UserPatch) **string { return &p.LastName }},
	{"nickname", "nickname", func(p *model.UserPatch) **string { return &p.Nickname }},
	{"position", "position", func(p *model.UserPatch) **string { return &p.Position }},
	{"username", "username", func(p *model.UserPatch) **string { return &p.Username }},
}

func newProfileEditCmd(app *App) *cobra.Command {
	vals := make([]string, len(profileFields))
	var tz string
	cmd := &cobra.Command{
		Use:         "edit [flags]",
		Annotations: effect(effectServer),
		Short:       "Change your names, position or time zone",
		Long: `Change fields of your profile; only the flags given change, an empty value
clears a field (except the username). --timezone takes an IANA name such as
Europe/Berlin, which sets the time zone by hand, or "automatic", which makes
it follow the machine: mm-cli sends the local IANA zone, as the app sends
the browser's.

The email and the password are changed in the app: mm-cli does not offer
them.

With --json it prints the changed profile (mm-cli profile --help). Exit code
2 without any flag, for an empty username, an unknown time zone, or
"automatic" when the local zone has no IANA name (then name it); 4 when the
server refuses to change a field.`,
		Example: `  mm-cli profile edit --first-name Alice --last-name Smith
  mm-cli profile edit --nickname "" --position "Release manager"
  mm-cli profile edit --timezone Europe/Berlin --json`,
		Args: argsRange(0, 0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			f := cmd.Flags()
			patch := &model.UserPatch{}
			changed := f.Changed("timezone")
			for i, pf := range profileFields {
				if f.Changed(pf.flag) {
					v := vals[i]
					*pf.field(patch) = &v
					changed = true
				}
			}
			if patch.Username != nil && strings.TrimSpace(*patch.Username) == "" {
				return clierr.Usage("the username cannot be empty")
			}
			if f.Changed("timezone") {
				if tz != automaticTimezone {
					if err := checkIANA(tz); err != nil {
						return err
					}
				} else if localIANA(app) == "" {
					return clierr.Usage("the local time zone has no IANA name; pass one, such as --timezone Europe/Berlin")
				}
			}
			if !changed {
				return clierr.Usage("nothing to change: pass at least one flag (mm-cli profile edit --help)")
			}
			s, err := app.openSession()
			if err != nil {
				return err
			}
			defer s.Close()
			ctx := cmd.Context()
			if f.Changed("timezone") {
				me, resp, err := s.Client.GetMe(ctx, "")
				if err != nil {
					return clierr.API(resp, err)
				}
				patch.Timezone = timezonePatch(me.Timezone, tz, localIANA(app))
			}
			u, resp, err := s.Client.PatchUser(ctx, s.Server.UserID, patch)
			if err != nil {
				return clierr.API(resp, err)
			}
			return app.printProfile(toProfile(u))
		},
	}
	f := cmd.Flags()
	for i, pf := range profileFields {
		f.StringVar(&vals[i], pf.flag, "", pf.help)
	}
	f.StringVar(&tz, "timezone", "", `IANA time zone, or "automatic"`)
	return cmd
}

// checkIANA checks an IANA time zone name.
func checkIANA(name string) error {
	if name == "" || name == "Local" {
		return clierr.Usage("invalid time zone %q: use an IANA name such as Europe/Berlin", name)
	}
	if _, err := time.LoadLocation(name); err != nil {
		return clierr.Usage("unknown time zone %q: use an IANA name such as Europe/Berlin", name)
	}
	return nil
}

// localIANA returns the IANA name of the local time zone, or "" when it has
// none: $TZ, else the name of the App's location, else the target of
// /etc/localtime.
func localIANA(app *App) string {
	if tz := app.Env("TZ"); tz != "" {
		if checkIANA(strings.TrimPrefix(tz, ":")) == nil {
			return strings.TrimPrefix(tz, ":")
		}
	}
	if name := app.Location.String(); name != "" && name != "Local" {
		return name
	}
	if target, err := os.Readlink("/etc/localtime"); err == nil {
		if _, name, ok := strings.Cut(target, "zoneinfo/"); ok {
			if checkIANA(name) == nil {
				return name
			}
		}
	}
	return ""
}

// timezonePatch returns the timezone map of a profile edit: a manual zone, or
// automatic with the local zone. The other value is kept.
func timezonePatch(cur model.StringMap, tz, local string) model.StringMap {
	out := model.StringMap{
		"useAutomaticTimezone": "false",
		"automaticTimezone":    cur["automaticTimezone"],
		"manualTimezone":       cur["manualTimezone"],
	}
	if tz == automaticTimezone {
		out["useAutomaticTimezone"] = "true"
		out["automaticTimezone"] = local
	} else {
		out["manualTimezone"] = tz
	}
	return out
}

func newProfilePictureCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "picture",
		Short: "Set or reset your profile picture",
		Long: `Your profile picture: set uploads an image, reset goes back to the
generated default picture.`,
	}
	set := &cobra.Command{
		Use:         "set <file>",
		Annotations: effect(effectServer),
		Short:       "Upload an image as your profile picture",
		Long: `Upload a local image (PNG, JPEG, GIF or BMP, as the server accepts) as your
profile picture.

With --json it prints {"action": "picture_set"}. Exit code 2 for an
unreadable file; 1 when the server rejects the image.`,
		Example: `  mm-cli profile picture set photo.png
  mm-cli profile picture set photo.jpg --json`,
		Args: argsRange(1, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := os.ReadFile(args[0])
			if err != nil {
				return clierr.Usage("%v", err)
			}
			s, err := app.openSession()
			if err != nil {
				return err
			}
			defer s.Close()
			if resp, err := s.Client.SetProfileImage(cmd.Context(), s.Server.UserID, data); err != nil {
				return clierr.API(resp, err)
			}
			return app.printDone(actionResult{Action: "picture_set"}, "Profile picture set.")
		},
	}
	reset := &cobra.Command{
		Use:         "reset",
		Annotations: effect(effectServer),
		Short:       "Go back to the default profile picture",
		Long: `Remove your uploaded profile picture; the server shows the generated default
picture again.

With --json it prints {"action": "picture_reset"}.`,
		Example: `  mm-cli profile picture reset
  mm-cli profile picture reset --json`,
		Args: argsRange(0, 0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := app.openSession()
			if err != nil {
				return err
			}
			defer s.Close()
			if resp, err := s.Client.SetDefaultProfileImage(cmd.Context(), s.Server.UserID); err != nil {
				return clierr.API(resp, err)
			}
			return app.printDone(actionResult{Action: "picture_reset"}, "Profile picture reset.")
		},
	}
	cmd.AddCommand(set, reset)
	return cmd
}
