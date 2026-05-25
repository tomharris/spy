package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tomharris/spy/internal/resolve"
)

type usersResult struct {
	Users []resolve.User `json:"users"`
}

var usersCmd = &cobra.Command{
	Use:     "users",
	Aliases: []string{"u"},
	Short:   "List workspace users",
	RunE: func(cmd *cobra.Command, args []string) error {
		_, r, err := newClientResolver()
		if err != nil {
			return err
		}
		res, err := runUsers(cmd.Context(), r)
		if err != nil {
			return err
		}
		if flagJSON {
			return emitJSON(res)
		}
		for _, u := range res.Users {
			name := u.FriendlyName()
			handle := ""
			if u.DisplayName != "" {
				handle = " (@" + u.DisplayName + ")"
			}
			status := ""
			if u.StatusText != "" {
				status = "  — " + u.StatusText
			}
			fmt.Printf("%s%s  %s%s\n", name, handle, u.ID, status)
		}
		return nil
	},
}

func runUsers(ctx context.Context, r *resolve.Resolver) (*usersResult, error) {
	users, err := r.Users(ctx)
	if err != nil {
		return nil, err
	}
	return &usersResult{Users: users}, nil
}

func init() {
	rootCmd.AddCommand(usersCmd)
}
