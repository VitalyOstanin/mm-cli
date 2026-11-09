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

// defaultProfile is the --json output of profile default without a name.
type defaultProfile struct {
	Default string `json:"default"`
}

func newProfileCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "profile",
		Short: "List, choose and remove profiles",
		Long: `Profiles keep several servers side by side: each has a server URL, a default
team, an optional rate limit and its own session (mm-cli help config).
mm-cli auth login creates them; --profile or $MM_PROFILE picks one per
command, otherwise the default profile is used.`,
	}
	cmd.AddCommand(newProfileListCmd(app), newProfileDefaultCmd(app), newProfileRemoveCmd(app))
	return cmd
}

type profileSummary struct {
	Name     string `json:"name"`
	URL      string `json:"url"`
	Username string `json:"username"`
	Team     string `json:"team"`
	Default  bool   `json:"default"`
}

func newProfileListCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List profiles; the default one is marked with *",
		Long: `List the local profiles, sorted by name; the default one is marked with *.
Reads only the local configuration, no request to a server.

With --json it prints an array of {"name": ..., "url": ..., "username": ...,
"team": ..., "default": true|false}; [] when there are no profiles.`,
		Example: `  mm-cli profile list
  mm-cli profile list --json`,
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
				p := cfg.Profiles[n]
				out = append(out, profileSummary{Name: n, URL: p.URL, Username: p.Username, Team: p.Team, Default: n == cfg.Default})
			}
			if app.json {
				return app.printJSON(out)
			}
			if len(out) == 0 {
				fmt.Fprintln(app.Out, "No profiles. Run `mm-cli auth login <url>`.")
				return nil
			}
			for _, p := range out {
				mark := " "
				if p.Default {
					mark = "*"
				}
				fmt.Fprintf(app.Out, "%s %s  %s  @%s  team %s\n", mark, p.Name, p.URL, p.Username, p.Team)
			}
			return nil
		},
	}
}

func newProfileDefaultCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "default [<name>]",
		Short: "Show or set the default profile",
		Long: `Without a name, show the default profile; with a name, make that profile the
default. Changes only the local configuration.

With --json it prints {"default": "<name>"}, or after setting
{"action": "default_set", "profile": "<name>"}. Exit code 2 when the profile
does not exist, or when no default is set and no name is given.`,
		Example: `  mm-cli profile default
  mm-cli profile default chat-example-com
  mm-cli profile default --json`,
		Args: argsRange(0, 1),
		RunE: func(_ *cobra.Command, args []string) error {
			cfg, err := app.loadConfig()
			if err != nil {
				return err
			}
			if len(args) == 0 {
				if cfg.Default == "" {
					return clierr.Usage("no default profile; set one with `mm-cli profile default <name>`")
				}
				if app.json {
					return app.printJSON(defaultProfile{Default: cfg.Default})
				}
				fmt.Fprintln(app.Out, cfg.Default)
				return nil
			}
			if _, ok := cfg.Profiles[args[0]]; !ok {
				return clierr.Usage("profile %q not found", args[0])
			}
			cfg.Default = args[0]
			if err := app.Store.Save(cfg); err != nil {
				return configErr(err)
			}
			return app.printDone(actionResult{Action: "default_set", Profile: args[0]}, "Default profile: %s", args[0])
		},
	}
}

func newProfileRemoveCmd(app *App) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a profile, its local session and cache",
		Long: `Remove a profile with its local session file and cache. The session on the
server stays valid until it expires; run 'mm-cli auth logout' first to end it.
If it was the default profile, no default is left.

Asks for confirmation on a terminal; without a terminal it needs --yes,
otherwise exit code 2. Exit code 2 also when the profile does not exist.
With --json it prints {"action": "removed", "profile": "<name>"}.`,
		Example: `  mm-cli profile remove chat-example-com
  mm-cli profile remove chat-example-com --yes --json`,
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
			if err := app.confirm(cmd.Context(), fmt.Sprintf("Remove profile %s?", name), yes); err != nil {
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
			return app.printDone(actionResult{Action: "removed", Profile: name}, "Removed profile %s.", name)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask for confirmation")
	return cmd
}
