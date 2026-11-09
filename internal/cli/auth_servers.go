package cli

import (
	"errors"
	"fmt"

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

// serverSummary is one saved server in the output of auth list and
// auth current. Field names are a public interface.
type serverSummary struct {
	Name     string `json:"name"`
	URL      string `json:"url"`
	Username string `json:"username"`
	Team     string `json:"team"`
	// Current marks the server set with auth set, used when neither
	// --server nor $MM_SERVER names another.
	Current bool `json:"current"`
}

func summarize(cfg *config.Config, name string) serverSummary {
	srv := cfg.Servers[name]
	return serverSummary{Name: name, URL: srv.URL, Username: srv.Username, Team: srv.Team, Current: name == cfg.Current}
}

func printServerLine(app *App, srv serverSummary) {
	mark := " "
	if srv.Current {
		mark = "*"
	}
	fmt.Fprintf(app.Out, "%s %s  %s  @%s  team %s\n", mark, srv.Name, srv.URL, srv.Username, srv.Team)
}

func newAuthListCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:         "list",
		Annotations: effect(effectRead),
		Short:       "List saved servers; the current one is marked with *",
		Long: `List the saved servers by their names, sorted; the current one,
chosen with mm-cli auth set, is marked with *. Reads only the local
configuration, no request to a server.

With --json it prints an array of {"name": ..., "url": ..., "username": ...,
"team": ..., "current": true|false}; [] when no server is saved.`,
		Example: `  mm-cli auth list
  mm-cli auth list --json`,
		Args: argsRange(0, 0),
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, err := app.loadConfig()
			if err != nil {
				return err
			}
			names := cfg.Names()
			out := make([]serverSummary, 0, len(names))
			for _, n := range names {
				out = append(out, summarize(cfg, n))
			}
			if app.json {
				return app.printJSON(out)
			}
			if len(out) == 0 {
				fmt.Fprintln(app.Out, "No servers saved. Run `mm-cli auth login <url>`.")
				return nil
			}
			for _, srv := range out {
				printServerLine(app, srv)
			}
			return nil
		},
	}
}

func newAuthCurrentCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:         "current",
		Annotations: effect(effectRead),
		Short:       "Show the server that commands use",
		Long: `Show the saved server that commands use now: the one named by --server, else
by $MM_SERVER, else the current server set with mm-cli auth set (the only
server when there is one). Reads only the local configuration.

With --json it prints {"name": ..., "url": ..., "username": ..., "team": ...,
"current": true|false}, where "current" tells whether it is the server set
with auth set. Exit code 3 when no server is saved; 2 when the named server
does not exist, or there are several and none is current.`,
		Example: `  mm-cli auth current
  mm-cli auth current --server work --json`,
		Args: argsRange(0, 0),
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, err := app.loadConfig()
			if err != nil {
				return err
			}
			name, _, err := cfg.Select(app.server, app.Env("MM_SERVER"))
			if err != nil {
				return configErr(err)
			}
			out := summarize(cfg, name)
			if app.json {
				return app.printJSON(out)
			}
			printServerLine(app, out)
			return nil
		},
	}
}

func newAuthSetCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:         "set <name>",
		Annotations: effect(effectLocal),
		Short:       "Make a saved server the current one",
		Long: `Make a saved server the current one: commands use it unless --server or
$MM_SERVER names another. Changes only the local configuration.

With --json it prints {"action": "current_set", "server": "<name>"}. Exit
code 2 when no server is saved under the name.`,
		Example: `  mm-cli auth set work
  mm-cli auth set work --json`,
		Args: argsRange(1, 1),
		RunE: func(_ *cobra.Command, args []string) error {
			cfg, err := app.loadConfig()
			if err != nil {
				return err
			}
			if _, ok := cfg.Servers[args[0]]; !ok {
				return clierr.Usage("server %q not found; see mm-cli auth list", args[0])
			}
			cfg.Current = args[0]
			if err := app.Store.Save(cfg); err != nil {
				return configErr(err)
			}
			return app.printDone(actionResult{Action: "current_set", Server: args[0]}, "Current server: %s", args[0])
		},
	}
}

func newAuthDeleteCmd(app *App) *cobra.Command {
	var confirmed bool
	cmd := &cobra.Command{
		Use:         "delete <name>",
		Annotations: effect(effectLocal),
		Short:       "Delete a saved server, its local session and cache",
		Long: `Delete a saved server with its local session file and cache. The session on
the server stays valid until it expires; run 'mm-cli auth logout' first to
end it; the text output reminds of this when a session was saved. If it was
the current server, none is current afterwards.

Asks for confirmation on a terminal; without a terminal it needs --confirm,
otherwise exit code 2. Exit code 2 also when no server is saved under the
name. With --json it prints {"action": "deleted", "server": "<name>"}.`,
		Example: `  mm-cli auth delete work
  mm-cli auth delete work --confirm --json`,
		Args: argsRange(1, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			cfg, err := app.loadConfig()
			if err != nil {
				return err
			}
			if _, ok := cfg.Servers[name]; !ok {
				return clierr.Usage("server %q not found; see mm-cli auth list", name)
			}
			if err := app.confirm(cmd.Context(), fmt.Sprintf("Delete saved server %s?", name), confirmed); err != nil {
				return err
			}
			// Read again: the confirmation may have waited a long time.
			if cfg, err = app.loadConfig(); err != nil {
				return err
			}
			// Only the file is looked at, never the server: the reminder
			// below must not cost a request.
			_, sessErr := app.Store.LoadSession(name)
			hadSession := sessErr == nil
			srvURL := cfg.Servers[name].URL
			delete(cfg.Servers, name)
			if cfg.Current == name {
				cfg.Current = ""
			}
			if err := app.Store.Save(cfg); err != nil {
				return configErr(err)
			}
			// Remove every file of the server, even when one of them fails,
			// so that a failure leaves as little stale state as possible.
			if err := errors.Join(app.Store.DeleteSession(name), app.Store.DeletePending(name),
				app.Store.DeleteCache(name)); err != nil {
				return configErr(err)
			}
			if err := app.printDone(actionResult{Action: "deleted", Server: name}, "Deleted saved server %s.", name); err != nil {
				return err
			}
			if hadSession && !app.json {
				fmt.Fprintf(app.Err, "mm-cli: the session on %s stays valid until it expires; "+
					"run mm-cli auth logout --server %s before deleting to end it\n", srvURL, name)
			}
			return nil
		},
	}
	addConfirmFlag(cmd, &confirmed)
	return cmd
}
