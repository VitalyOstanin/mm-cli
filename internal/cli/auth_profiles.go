package cli

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/VitalyOstanin/mm-cli/internal/clierr"
	"github.com/VitalyOstanin/mm-cli/internal/config"
)

// loadConfig reads config.yaml; a file that cannot be used as written is a
// usage error, a file that cannot be read a general error.
func (a *App) loadConfig() (*config.Config, error) {
	cfg, err := a.Store.Load()
	if err != nil {
		return nil, configErr(err)
	}
	return cfg, nil
}

// profileSummary is one saved connection in the output of auth list and
// auth current. Field names are a public interface.
type profileSummary struct {
	Name     string `json:"name"`
	URL      string `json:"url"`
	Username string `json:"username"`
	Team     string `json:"team"`
	// Current marks the profile set with auth set, used when neither
	// --profile nor $MM_PROFILE names another.
	Current bool `json:"current"`
}

func summarize(cfg *config.Config, name string) profileSummary {
	p := cfg.Profiles[name]
	return profileSummary{Name: name, URL: p.URL, Username: p.Username, Team: p.Team, Current: name == cfg.Default}
}

func printProfileLine(app *App, p profileSummary) {
	mark := " "
	if p.Current {
		mark = "*"
	}
	fmt.Fprintf(app.Out, "%s %s  %s  @%s  team %s\n", mark, p.Name, p.URL, p.Username, p.Team)
}

func newAuthListCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List saved profiles; the current one is marked with *",
		Long: `List the saved profiles (server connections), sorted by name; the current one,
chosen with mm-cli auth set, is marked with *. Reads only the local
configuration, no request to a server.

With --json it prints an array of {"name": ..., "url": ..., "username": ...,
"team": ..., "current": true|false}; [] when there are no profiles.`,
		Example: `  mm-cli auth list
  mm-cli auth list --json`,
		Args: argsRange(0, 0),
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, err := app.loadConfig()
			if err != nil {
				return err
			}
			names := make([]string, 0, len(cfg.Profiles))
			for n := range cfg.Profiles {
				names = append(names, n)
			}
			sort.Strings(names)
			out := make([]profileSummary, 0, len(names))
			for _, n := range names {
				out = append(out, summarize(cfg, n))
			}
			if app.json {
				return app.printJSON(out)
			}
			if len(out) == 0 {
				fmt.Fprintln(app.Out, "No profiles. Run `mm-cli auth login <url>`.")
				return nil
			}
			for _, p := range out {
				printProfileLine(app, p)
			}
			return nil
		},
	}
}

func newAuthCurrentCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "current",
		Short: "Show the profile that commands use",
		Long: `Show the profile that commands use now: the one named by --profile, else by
$MM_PROFILE, else the current profile set with mm-cli auth set (the only
profile when there is one). Reads only the local configuration.

With --json it prints {"name": ..., "url": ..., "username": ..., "team": ...,
"current": true|false}, where "current" tells whether it is the profile set
with auth set. Exit code 3 when there are no profiles; 2 when the named
profile does not exist, or there are several and none is current.`,
		Example: `  mm-cli auth current
  mm-cli auth current --profile chat-example-com --json`,
		Args: argsRange(0, 0),
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, err := app.loadConfig()
			if err != nil {
				return err
			}
			name, _, err := cfg.Select(app.profile, app.Env("MM_PROFILE"))
			if err != nil {
				return configErr(err)
			}
			out := summarize(cfg, name)
			if app.json {
				return app.printJSON(out)
			}
			printProfileLine(app, out)
			return nil
		},
	}
}

func newAuthSetCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "set <name>",
		Short: "Make a profile the current one",
		Long: `Make a saved profile the current one: commands use it unless --profile or
$MM_PROFILE names another. Changes only the local configuration.

With --json it prints {"action": "current_set", "profile": "<name>"}. Exit
code 2 when the profile does not exist.`,
		Example: `  mm-cli auth set chat-example-com
  mm-cli auth set chat-example-com --json`,
		Args: argsRange(1, 1),
		RunE: func(_ *cobra.Command, args []string) error {
			cfg, err := app.loadConfig()
			if err != nil {
				return err
			}
			if _, ok := cfg.Profiles[args[0]]; !ok {
				return clierr.Usage("profile %q not found", args[0])
			}
			cfg.Default = args[0]
			if err := app.Store.Save(cfg); err != nil {
				return configErr(err)
			}
			return app.printDone(actionResult{Action: "current_set", Profile: args[0]}, "Current profile: %s", args[0])
		},
	}
}

func newAuthDeleteCmd(app *App) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a saved profile, its local session and cache",
		Long: `Delete a saved profile with its local session file and cache. The session on
the server stays valid until it expires; run 'mm-cli auth logout' first to
end it. If it was the current profile, none is current afterwards.

Asks for confirmation on a terminal; without a terminal it needs --yes,
otherwise exit code 2. Exit code 2 also when the profile does not exist.
With --json it prints {"action": "deleted", "profile": "<name>"}.`,
		Example: `  mm-cli auth delete chat-example-com
  mm-cli auth delete chat-example-com --yes --json`,
		Args: argsRange(1, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			cfg, err := app.loadConfig()
			if err != nil {
				return err
			}
			if _, ok := cfg.Profiles[name]; !ok {
				return clierr.Usage("profile %q not found", name)
			}
			if err := app.confirm(cmd.Context(), fmt.Sprintf("Delete profile %s?", name), yes); err != nil {
				return err
			}
			// Read again: the confirmation may have waited a long time.
			if cfg, err = app.loadConfig(); err != nil {
				return err
			}
			delete(cfg.Profiles, name)
			if cfg.Default == name {
				cfg.Default = ""
			}
			if err := app.Store.Save(cfg); err != nil {
				return configErr(err)
			}
			if err := app.Store.DeleteSession(name); err != nil {
				return configErr(err)
			}
			_ = app.Store.DeletePending(name)
			if err := app.Store.DeleteCache(name); err != nil {
				return configErr(err)
			}
			return app.printDone(actionResult{Action: "deleted", Profile: name}, "Deleted profile %s.", name)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask for confirmation")
	return cmd
}
