// mypal — autonomous messaging agent daemon.
//
// Usage: mypal [command] [flags]
//
// Commands:
//
//	config   Read or write configuration keys in the YAML (respects encryption)
//  daemon   Tool for install daemon services in the user folder
//	migrate  Migrate an OpenClaw config file to MyPal format
//	serve    Start the HTTP server and all messaging adapters (default)
//	version  Print build version and exit
//
// # License
// See LICENSE in the root of the repository.
package main

import (
	"embed"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	cmdconfig "github.com/BangRocket/MyPal/apps/backend/cmd/mypal/config"
	cmddaemon "github.com/BangRocket/MyPal/apps/backend/cmd/mypal/daemon"
	cmdmigrate "github.com/BangRocket/MyPal/apps/backend/cmd/mypal/migrate"
	cmdmigratememory "github.com/BangRocket/MyPal/apps/backend/cmd/mypal/migrate_memory"
	cmdserve "github.com/BangRocket/MyPal/apps/backend/cmd/mypal/serve"
	cmdversion "github.com/BangRocket/MyPal/apps/backend/cmd/mypal/version"
)

// version is set at build time via -ldflags "-X main.version=x.y.z"
var version = "dev"

// public is the embedded frontend + static assets.
//
//go:embed all:public
var public embed.FS

func main() {
	// Disable Ollama SDK key-based auth; we use Bearer token via our own transport.
	if os.Getenv("OLLAMA_AUTH") == "" {
		os.Setenv("OLLAMA_AUTH", "false")
	}

	root := &cobra.Command{
		Use:           "mypal",
		Short:         "Autonomous messaging agent daemon",
		SilenceUsage:  true,
		SilenceErrors: true,
		// Running "mypal" with no subcommand starts the server.
		Run: func(cmd *cobra.Command, args []string) {
			cmdserve.New(version, public).Run()
		},
	}

	root.AddCommand(
		cmdconfig.Command(),
		cmddaemon.Command(),
		cmdmigrate.Command(),
		cmdmigratememory.Command(),
		cmdserve.Command(version, public),
		cmdversion.Command(version),
	)

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
