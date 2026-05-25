package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tomharris/spy/internal/resolve"
)

// dmEntry pairs the channel with the resolved counterparty name so JSON
// consumers don't have to do a second lookup.
type dmEntry struct {
	ChannelID string `json:"channel_id"`
	UserID    string `json:"user_id"`
	UserName  string `json:"user_name"`
}

type dmsResult struct {
	DMs []dmEntry `json:"dms"`
}

var dmsCmd = &cobra.Command{
	Use:     "dms",
	Aliases: []string{"dm"},
	Short:   "List direct-message conversations",
	RunE: func(cmd *cobra.Command, args []string) error {
		_, r, err := newClientResolver()
		if err != nil {
			return err
		}
		res, err := runDMs(cmd.Context(), r)
		if err != nil {
			return err
		}
		if flagJSON {
			return emitJSON(res)
		}
		for _, dm := range res.DMs {
			fmt.Printf("%s  %s\n", dm.UserName, dm.ChannelID)
		}
		return nil
	},
}

func runDMs(ctx context.Context, r *resolve.Resolver) (*dmsResult, error) {
	channels, err := r.Channels(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]dmEntry, 0)
	for _, ch := range channels {
		if !ch.IsIM {
			continue
		}
		out = append(out, dmEntry{
			ChannelID: ch.ID,
			UserID:    ch.User,
			UserName:  r.UserName(ctx, ch.User),
		})
	}
	return &dmsResult{DMs: out}, nil
}

func init() {
	rootCmd.AddCommand(dmsCmd)
}
