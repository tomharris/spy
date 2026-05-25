package main

import (
	"context"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/tomharris/spy/internal/resolve"
	"github.com/tomharris/spy/internal/slack"
)

type threadResult struct {
	Channel  string    `json:"channel"`
	ThreadTS string    `json:"thread_ts"`
	Messages []message `json:"messages"`
}

var threadCmd = &cobra.Command{
	Use:     "thread <channel|@user> <ts> [count]",
	Aliases: []string{"t"},
	Short:   "Read replies in a thread",
	Args:    cobra.RangeArgs(2, 3),
	RunE: func(cmd *cobra.Command, args []string) error {
		count := 50
		if len(args) == 3 {
			n, err := strconv.Atoi(args[2])
			if err != nil || n <= 0 {
				return fmt.Errorf("count must be a positive integer, got %q", args[2])
			}
			count = n
		}

		client, r, err := newClientResolver()
		if err != nil {
			return err
		}
		res, err := runThread(cmd.Context(), client, r, args[0], args[1], count)
		if err != nil {
			return err
		}
		if flagJSON {
			return emitJSON(res)
		}
		for _, m := range res.Messages {
			printMessage(m, "", readShowTS)
			fmt.Println()
		}
		return nil
	},
}

// runThread returns every message in a thread (parent + replies).
func runThread(ctx context.Context, client *slack.Client, r *resolve.Resolver, channelRef, ts string, count int) (*threadResult, error) {
	channelID, err := r.ResolveChannel(ctx, channelRef)
	if err != nil {
		return nil, err
	}
	var rep repliesResponse
	if err := client.Call(ctx, "conversations.replies", map[string]any{
		"channel": channelID,
		"ts":      ts,
		"limit":   count,
	}, &rep); err != nil {
		return nil, err
	}
	out := make([]message, 0, len(rep.Messages))
	for _, raw := range rep.Messages {
		out = append(out, toMessage(ctx, r, raw))
	}
	return &threadResult{Channel: channelID, ThreadTS: ts, Messages: out}, nil
}

func init() {
	rootCmd.AddCommand(threadCmd)
}
