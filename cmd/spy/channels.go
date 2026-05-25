package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tomharris/spy/internal/resolve"
)

type channelsResult struct {
	Channels []resolve.Channel `json:"channels"`
}

var channelsCmd = &cobra.Command{
	Use:     "channels",
	Aliases: []string{"ch"},
	Short:   "List public and private channels",
	RunE: func(cmd *cobra.Command, args []string) error {
		_, r, err := newClientResolver()
		if err != nil {
			return err
		}
		res, err := runChannels(cmd.Context(), r)
		if err != nil {
			return err
		}
		if flagJSON {
			return emitJSON(res)
		}
		for _, ch := range res.Channels {
			prefix := "#"
			if ch.IsPrivate {
				prefix = "🔒"
			}
			members := ""
			if ch.NumMembers > 0 {
				members = fmt.Sprintf("  (%d members)", ch.NumMembers)
			}
			fmt.Printf("%s %s%s  %s\n", prefix, ch.Name, members, ch.ID)
		}
		return nil
	},
}

// runChannels filters the resolver's cached conversation list down to
// public + private channels (excluding DMs and group DMs, which have
// their own command).
func runChannels(ctx context.Context, r *resolve.Resolver) (*channelsResult, error) {
	all, err := r.Channels(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]resolve.Channel, 0, len(all))
	for _, ch := range all {
		if ch.IsIM || ch.IsMPIM {
			continue
		}
		out = append(out, ch)
	}
	return &channelsResult{Channels: out}, nil
}

func init() {
	rootCmd.AddCommand(channelsCmd)
}
