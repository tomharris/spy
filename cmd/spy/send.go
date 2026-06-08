package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tomharris/spy/internal/resolve"
	"github.com/tomharris/spy/internal/slack"
)

var sendThreadTS string

type sendResult struct {
	slack.BaseResponse
	Channel string `json:"channel"`
	TS      string `json:"ts"`
}

var sendCmd = &cobra.Command{
	Use:     "send <channel|@user> <message...>",
	Aliases: []string{"s"},
	Short:   "Send a message to a channel or DM",
	Args:    cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, r, err := newClientResolver()
		if err != nil {
			return err
		}
		text := strings.Join(args[1:], " ")
		res, err := runSend(cmd.Context(), client, r, args[0], text, sendThreadTS)
		if err != nil {
			return err
		}
		if flagJSON {
			return emitJSON(res)
		}
		fmt.Printf("sent to %s  (ts: %s)\n", args[0], res.TS)
		return nil
	},
}

func runSend(ctx context.Context, client *slack.Client, r *resolve.Resolver, channelRef, text, threadTS string) (*sendResult, error) {
	channelID, err := r.ResolveChannel(ctx, channelRef)
	if err != nil {
		return nil, err
	}
	params := map[string]any{
		"channel": channelID,
		"text":    text,
	}
	if threadTS != "" {
		params["thread_ts"] = threadTS
	}
	var res sendResult
	if err := client.Call(ctx, "chat.postMessage", params, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

func init() {
	sendCmd.Flags().StringVar(&sendThreadTS, "thread", "", "reply in this thread (parent message ts)")
	rootCmd.AddCommand(sendCmd)
}
