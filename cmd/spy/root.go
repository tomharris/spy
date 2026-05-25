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
	flagJSON    bool
	flagRefresh bool
)

var rootCmd = &cobra.Command{
	Use:           "spy",
	Short:         "Slack CLI for macOS — auto-auth from the Slack desktop app",
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	rootCmd.PersistentFlags().BoolVar(&flagJSON, "json", false, "emit JSON output")
	rootCmd.PersistentFlags().BoolVar(&flagRefresh, "refresh", false, "skip cached token and re-extract from Slack")
}

// Execute is the entry point called from main().
func Execute() error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	return rootCmd.ExecuteContext(ctx)
}

// newClient builds an authenticated Slack client honoring --refresh.
// Subcommands call this lazily so `spy --help` doesn't touch the keychain.
func newClient() (*slack.Client, error) {
	src, err := auth.DefaultSource()
	if err != nil {
		return nil, err
	}
	return slack.NewClient(src, flagRefresh)
}

// emitJSON writes v as indented JSON to stdout. Used by every subcommand's
// --json branch.
func emitJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
