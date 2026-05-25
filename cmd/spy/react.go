package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tomharris/spy/internal/resolve"
	"github.com/tomharris/spy/internal/slack"
)

type reactResult struct {
	slack.BaseResponse
	Channel string `json:"channel"`
	TS      string `json:"ts"`
	Emoji   string `json:"emoji"`
}

var reactCmd = &cobra.Command{
	Use:   "react <channel|@user> <ts> <emoji>",
	Short: "Add an emoji reaction to a message",
	Args:  cobra.ExactArgs(3),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, r, err := newClientResolver()
		if err != nil {
			return err
		}
		res, err := runReact(cmd.Context(), client, r, args[0], args[1], args[2])
		if err != nil {
			return err
		}
		if flagJSON {
			return emitJSON(res)
		}
		fmt.Printf("reacted :%s: on %s @ %s\n", res.Emoji, args[0], res.TS)
		return nil
	},
}

func runReact(ctx context.Context, client *slack.Client, r *resolve.Resolver, channelRef, ts, emoji string) (*reactResult, error) {
	channelID, err := r.ResolveChannel(ctx, channelRef)
	if err != nil {
		return nil, err
	}
	name := strings.Trim(emoji, ":")
	res := reactResult{Channel: channelID, TS: ts, Emoji: name}
	if err := client.Call(ctx, "reactions.add", map[string]any{
		"channel":   channelID,
		"timestamp": ts,
		"name":      name,
	}, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

func init() {
	rootCmd.AddCommand(reactCmd)
}
