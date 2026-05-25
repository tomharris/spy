package main

import (
	"context"
	"encoding/json"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/tomharris/spy/internal/auth"
	"github.com/tomharris/spy/internal/slack"
)

var (
	flagJSON      bool
	flagRefresh   bool
	flagWorkspace string
)

const workspaceEnvVar = "SPY_WORKSPACE"

var rootCmd = &cobra.Command{
	Use:           "spy",
	Short:         "Slack CLI for macOS — auto-auth from the Slack desktop app",
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	rootCmd.PersistentFlags().BoolVar(&flagJSON, "json", false, "emit JSON output")
	rootCmd.PersistentFlags().BoolVar(&flagRefresh, "refresh", false, "force re-discovery of workspaces (bypasses cache)")
	rootCmd.PersistentFlags().StringVarP(&flagWorkspace, "workspace", "w", "",
		"workspace to target — team domain or team_id (env: "+workspaceEnvVar+")")
}

// Execute is the entry point called from main().
func Execute() error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	return rootCmd.ExecuteContext(ctx)
}

// newClient resolves the target workspace (--workspace > SPY_WORKSPACE >
// configured default > only signed-in workspace) and returns a Slack
// client bound to it. Honors --refresh by pre-discovering workspaces.
//
// Subcommands call this lazily so `spy --help` never touches the keychain.
func newClient() (*slack.Client, error) {
	src, err := auth.DefaultSource()
	if err != nil {
		return nil, err
	}
	if flagRefresh {
		if _, err := auth.ListWorkspaces(src, true); err != nil {
			return nil, err
		}
	}
	ws, err := resolveTargetWorkspace(src)
	if err != nil {
		return nil, err
	}
	return slack.NewClient(src, ws)
}

func resolveTargetWorkspace(src *auth.Source) (*auth.Workspace, error) {
	id := flagWorkspace
	if id == "" {
		id = os.Getenv(workspaceEnvVar)
	}
	if id != "" {
		return auth.ResolveWorkspace(src, id)
	}
	return auth.DefaultWorkspace(src)
}

// emitJSON writes v as indented JSON to stdout. Used by every subcommand's
// --json branch.
func emitJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
