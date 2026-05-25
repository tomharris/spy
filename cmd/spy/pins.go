package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tomharris/spy/internal/resolve"
	"github.com/tomharris/spy/internal/slack"
)

// pinsListResponse mirrors the pins.list payload. Pinned items can be
// messages or files; we only render messages but the
// raw struct preserves the type field for JSON consumers.
type pinsListResponse struct {
	slack.BaseResponse
	Items []pinItem `json:"items"`
}

type pinItem struct {
	Type    string `json:"type"`
	Channel string `json:"channel"`
	Message struct {
		TS   string `json:"ts"`
		User string `json:"user"`
		Text string `json:"text"`
	} `json:"message"`
	File struct {
		Name     string `json:"name"`
		Mimetype string `json:"mimetype"`
	} `json:"file"`
}

type pinEntry struct {
	Type      string `json:"type"`
	Timestamp string `json:"ts,omitempty"`
	UserID    string `json:"user_id,omitempty"`
	UserName  string `json:"user_name,omitempty"`
	Text      string `json:"text,omitempty"`
	FileName  string `json:"file_name,omitempty"`
}

type pinsResult struct {
	Channel string     `json:"channel"`
	Pins    []pinEntry `json:"pins"`
}

var pinsCmd = &cobra.Command{
	Use:     "pins <channel|@user>",
	Aliases: []string{"pin"},
	Short:   "List pinned items in a channel or DM",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, r, err := newClientResolver()
		if err != nil {
			return err
		}
		res, err := runPins(cmd.Context(), client, r, args[0])
		if err != nil {
			return err
		}
		if flagJSON {
			return emitJSON(res)
		}
		if len(res.Pins) == 0 {
			fmt.Println("No pinned items.")
			return nil
		}
		fmt.Printf("📌 %d pinned items:\n\n", len(res.Pins))
		for _, p := range res.Pins {
			if p.Type == "file" {
				fmt.Printf("📎 %s\n\n", p.FileName)
				continue
			}
			fmt.Printf("[%s] %s:\n  %s\n\n", formatTS(p.Timestamp), p.UserName, p.Text)
		}
		return nil
	},
}

func runPins(ctx context.Context, client *slack.Client, r *resolve.Resolver, channelRef string) (*pinsResult, error) {
	channelID, err := r.ResolveChannel(ctx, channelRef)
	if err != nil {
		return nil, err
	}
	var res pinsListResponse
	if err := client.Call(ctx, "pins.list", map[string]any{"channel": channelID}, &res); err != nil {
		return nil, err
	}
	out := make([]pinEntry, 0, len(res.Items))
	for _, item := range res.Items {
		entry := pinEntry{Type: item.Type}
		switch item.Type {
		case "message":
			entry.Timestamp = item.Message.TS
			entry.UserID = item.Message.User
			entry.UserName = r.UserName(ctx, item.Message.User)
			entry.Text = item.Message.Text
		case "file":
			entry.FileName = item.File.Name
		}
		out = append(out, entry)
	}
	return &pinsResult{Channel: channelID, Pins: out}, nil
}

func init() {
	rootCmd.AddCommand(pinsCmd)
}
