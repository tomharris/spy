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
		return reactionCommand(cmd.Context(), "reactions.add", "already_reacted", "reacted", args)
	},
}

var unreactCmd = &cobra.Command{
	Use:   "unreact <channel|@user> <ts> <emoji>",
	Short: "Remove your emoji reaction from a message",
	Args:  cobra.ExactArgs(3),
	RunE: func(cmd *cobra.Command, args []string) error {
		return reactionCommand(cmd.Context(), "reactions.remove", "no_reaction", "unreacted", args)
	},
}

func reactionCommand(ctx context.Context, method, noop, verb string, args []string) error {
	client, r, err := newClientResolver()
	if err != nil {
		return err
	}
	res, err := runReaction(ctx, client, r, method, noop, args[0], args[1], args[2])
	if err != nil {
		return err
	}
	if flagJSON {
		return emitJSON(res)
	}
	fmt.Printf("%s :%s: on %s @ %s\n", verb, res.Emoji, args[0], res.TS)
	return nil
}

// runReaction treats `noop` (already_reacted / no_reaction) as success, so
// adding or removing a reaction is idempotent.
func runReaction(ctx context.Context, client *slack.Client, r *resolve.Resolver, method, noop, channelRef, ts, emoji string) (*reactResult, error) {
	channelID, err := r.ResolveChannel(ctx, channelRef)
	if err != nil {
		return nil, err
	}
	name := strings.Trim(emoji, ":")
	res := reactResult{Channel: channelID, TS: ts, Emoji: name}
	if err := client.Call(ctx, method, map[string]any{
		"channel":   channelID,
		"timestamp": ts,
		"name":      name,
	}, &res); err != nil {
		if res.SlackError() != noop {
			return nil, err
		}
		res.Error = ""
		res.Ok = true
	}
	return &res, nil
}

func init() {
	rootCmd.AddCommand(reactCmd)
	rootCmd.AddCommand(unreactCmd)
}
