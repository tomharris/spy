package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/tomharris/spy/internal/auth"
)

var workspacesCmd = &cobra.Command{
	Use:   "workspaces",
	Short: "List signed-in Slack workspaces",
	Long: "List every Slack workspace currently signed in on this Mac.\n" +
		"The starred entry is the configured default (used when --workspace is omitted).",
	RunE: func(cmd *cobra.Command, args []string) error {
		src, err := auth.DefaultSource()
		if err != nil {
			return err
		}
		workspaces, err := auth.ListWorkspaces(src, flagRefresh)
		if err != nil {
			return err
		}
		cfg, _ := auth.LoadConfig(src)
		defaultID := ""
		if cfg != nil {
			defaultID = cfg.DefaultWorkspace
		}

		if flagJSON {
			return emitJSON(map[string]any{
				"workspaces": workspaces,
				"default":    defaultID,
			})
		}

		for _, ws := range workspaces {
			marker := "  "
			if ws.TeamID == defaultID {
				marker = "* "
			}
			fmt.Printf("%s%s  (%s)  — %s @ %s\n",
				marker, ws.TeamDomain, ws.TeamID, ws.UserName, ws.TeamName)
		}
		if defaultID == "" && len(workspaces) > 1 {
			fmt.Fprintf(os.Stderr,
				"\nNo default workspace set. Pass --workspace, set %s, or run `spy workspaces use <id>`.\n",
				workspaceEnvVar)
		}
		return nil
	},
}

var workspacesUseCmd = &cobra.Command{
	Use:   "use <team_id|domain>",
	Short: "Set the default workspace",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		src, err := auth.DefaultSource()
		if err != nil {
			return err
		}
		ws, err := auth.ResolveWorkspace(src, args[0])
		if err != nil {
			return err
		}
		cfg, err := auth.LoadConfig(src)
		if err != nil {
			return err
		}
		cfg.DefaultWorkspace = ws.TeamID
		if err := auth.SaveConfig(src, cfg); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "default workspace set to %s (%s)\n", ws.TeamDomain, ws.TeamID)
		return nil
	},
}

var workspacesRefreshCmd = &cobra.Command{
	Use:   "refresh",
	Short: "Re-extract all workspace tokens from the Slack desktop app",
	RunE: func(cmd *cobra.Command, args []string) error {
		src, err := auth.DefaultSource()
		if err != nil {
			return err
		}
		workspaces, err := auth.ListWorkspaces(src, true)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "refreshed %d workspace(s)\n", len(workspaces))
		return nil
	},
}

func init() {
	rootCmd.AddCommand(workspacesCmd)
	workspacesCmd.AddCommand(workspacesUseCmd)
	workspacesCmd.AddCommand(workspacesRefreshCmd)
}
