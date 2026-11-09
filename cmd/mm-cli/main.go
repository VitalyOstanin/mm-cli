// Command mm-cli manages Mattermost messages, files and channels.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	// The time zone database, for systems without one (Windows, minimal
	// containers): profile zones and <when> times need it.
	_ "time/tzdata"

	"github.com/VitalyOstanin/mm-cli/internal/cli"
	"github.com/VitalyOstanin/mm-cli/internal/clierr"
)

const defaultVersion = "dev"

// version is set at build time with -ldflags "-X main.version=...".
var version = defaultVersion

// resolveVersion returns the ldflags version when it was set. Otherwise it
// falls back to the module version recorded by "go install ...@vX.Y.Z".
func resolveVersion(ldflagsVersion string, info *debug.BuildInfo, ok bool) string {
	if ldflagsVersion != defaultVersion || !ok || info == nil {
		return ldflagsVersion
	}
	if v := info.Main.Version; v != "" && v != "(devel)" {
		return v
	}
	return ldflagsVersion
}

func main() {
	ctx, stop := signalContext()
	info, ok := debug.ReadBuildInfo()
	app, err := cli.NewApp(resolveVersion(version, info, ok))
	if err != nil {
		fmt.Fprintln(os.Stderr, "mm-cli:", err)
		stop()
		os.Exit(clierr.ExitCode(err))
	}
	code := cli.Execute(ctx, app, os.Args[1:])
	stop()
	os.Exit(code)
}

// signalContext returns a context cancelled by SIGINT or SIGTERM, with the
// signal recorded as the cause so that the exit code can tell them apart.
func signalContext() (context.Context, func()) {
	ctx, cancel := context.WithCancelCause(context.Background())
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	go func() {
		select {
		case sig := <-sigs:
			if sig == syscall.SIGTERM {
				cancel(clierr.Terminated())
			} else {
				cancel(clierr.Interrupted())
			}
		case <-ctx.Done():
		}
	}()
	return ctx, func() {
		signal.Stop(sigs)
		cancel(nil)
	}
}
