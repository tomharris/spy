package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tomharris/spy/internal/slack"
)

// authTestResponse mirrors the fields we surface from auth.test.
// Embeds slack.BaseResponse so slack.Client can read ok/error.
type authTestResponse struct {
	slack.BaseResponse
	URL    string `json:"url,omitempty"`
	Team   string `json:"team,omitempty"`
	User   string `json:"user,omitempty"`
	TeamID string `json:"team_id,omitempty"`
	UserID string `json:"user_id,omitempty"`
}

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Test auth and print user/team info",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := newClient()
		if err != nil {
			return err
		}
		res, err := runAuth(cmd.Context(), client)
		if err != nil {
			return err
		}
		if flagJSON {
			return emitJSON(res)
		}
		fmt.Printf("authenticated as %s @ %s\n", res.User, res.Team)
		fmt.Printf("  team id: %s\n", res.TeamID)
		fmt.Printf("  user id: %s\n", res.UserID)
		fmt.Printf("  url:     %s\n", res.URL)
		return nil
	},
}

// runAuth is the pure-logic entry — callable from cobra and (later) MCP
// without duplicating the API call.
func runAuth(ctx context.Context, client *slack.Client) (*authTestResponse, error) {
	var res authTestResponse
	if err := client.Call(ctx, "auth.test", nil, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

func init() {
	rootCmd.AddCommand(authCmd)
}
