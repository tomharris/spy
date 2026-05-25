package main

import (
	"context"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/tomharris/spy/internal/resolve"
	"github.com/tomharris/spy/internal/slack"
)

var savedIncludeCompleted bool

// savedListResponse mirrors saved.list — the undocumented endpoint that
// backs the "Saved for later" sidebar in the Slack client. Each item is
// a (channel_id, ts) pair; the actual message text has to be fetched
// separately via conversations.history.
type savedListResponse struct {
	slack.BaseResponse
	SavedItems []savedItem `json:"saved_items"`
	Counts     struct {
		UncompletedCount int `json:"uncompleted_count"`
		CompletedCount   int `json:"completed_count"`
	} `json:"counts"`
}

type savedItem struct {
	ItemID      string  `json:"item_id"`
	TS          string  `json:"ts"`
	State       string  `json:"state"`
	DateCreated float64 `json:"date_created"`
}

type savedEntry struct {
	ChannelID    string `json:"channel_id"`
	ChannelName  string `json:"channel_name,omitempty"`
	Timestamp    string `json:"ts"`
	State        string `json:"state"`
	SavedAt      string `json:"saved_at"`
	UserID       string `json:"user_id,omitempty"`
	UserName     string `json:"user_name,omitempty"`
	Text         string `json:"text,omitempty"`
	MessageTS    string `json:"message_ts,omitempty"`
	FetchError   string `json:"fetch_error,omitempty"`
}

type savedResult struct {
	UncompletedCount int          `json:"uncompleted_count"`
	CompletedCount   int          `json:"completed_count"`
	Items            []savedEntry `json:"items"`
}

var savedCmd = &cobra.Command{
	Use:     "saved [count]",
	Aliases: []string{"sv"},
	Short:   "Show Saved for Later items",
	Args:    cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		count := 20
		if len(args) == 1 {
			n, err := strconv.Atoi(args[0])
			if err != nil || n <= 0 {
				return fmt.Errorf("count must be a positive integer, got %q", args[0])
			}
			count = n
		}
		client, r, err := newClientResolver()
		if err != nil {
			return err
		}
		res, err := runSaved(cmd.Context(), client, r, count, savedIncludeCompleted)
		if err != nil {
			return err
		}
		if flagJSON {
			return emitJSON(res)
		}
		fmt.Printf("📑 Saved for Later — %d active, %d completed\n\n",
			res.UncompletedCount, res.CompletedCount)
		if len(res.Items) == 0 {
			fmt.Println("No saved items.")
			return nil
		}
		for _, it := range res.Items {
			ch := it.ChannelName
			if ch == "" {
				ch = it.ChannelID
			}
			state := ""
			if it.State == "completed" {
				state = " ✅"
			}
			if it.FetchError != "" {
				fmt.Printf("[saved %s]%s #%s (ts: %s) — %s\n\n",
					it.SavedAt, state, ch, it.Timestamp, it.FetchError)
				continue
			}
			fmt.Printf("[saved %s]%s #%s — %s (%s):\n  %s\n\n",
				it.SavedAt, state, ch, it.UserName, formatTS(it.MessageTS),
				truncateRunes(it.Text, 300))
		}
		return nil
	},
}

func runSaved(ctx context.Context, client *slack.Client, r *resolve.Resolver, count int, includeCompleted bool) (*savedResult, error) {
	channels, err := r.Channels(ctx)
	if err != nil {
		return nil, err
	}
	chMap := make(map[string]resolve.Channel, len(channels))
	for _, ch := range channels {
		chMap[ch.ID] = ch
	}

	var res savedListResponse
	if err := client.Call(ctx, "saved.list", map[string]any{"count": count}, &res); err != nil {
		return nil, err
	}

	out := &savedResult{
		UncompletedCount: res.Counts.UncompletedCount,
		CompletedCount:   res.Counts.CompletedCount,
	}
	for _, item := range res.SavedItems {
		if !includeCompleted && item.State == "completed" {
			continue
		}
		entry := savedEntry{
			ChannelID:   item.ItemID,
			ChannelName: channelLabel(chMap, item.ItemID, r, ctx),
			Timestamp:   item.TS,
			State:       item.State,
			SavedAt:     formatTSFloat(item.DateCreated),
		}
		// Fetch the actual message. The user may have lost access to the
		// channel — surface that as a fetch_error rather than aborting the
		// whole command.
		var hist historyResponse
		err := client.Call(ctx, "conversations.history", map[string]any{
			"channel":   item.ItemID,
			"latest":    item.TS,
			"inclusive": true,
			"limit":     1,
		}, &hist)
		if err != nil || len(hist.Messages) == 0 {
			entry.FetchError = "could not fetch message"
			if err != nil {
				entry.FetchError = err.Error()
			}
			out.Items = append(out.Items, entry)
			continue
		}
		m := hist.Messages[0]
		entry.MessageTS = m.TS
		entry.UserID = m.User
		entry.UserName = r.UserName(ctx, m.User)
		entry.Text = m.Text
		out.Items = append(out.Items, entry)
	}
	return out, nil
}

// formatTSFloat is the float64 variant of formatTS — saved items report
// date_created as a number, not a string.
func formatTSFloat(secs float64) string {
	return formatTS(strconv.FormatFloat(secs, 'f', -1, 64))
}

func init() {
	savedCmd.Flags().BoolVar(&savedIncludeCompleted, "all", false, "include completed items")
	rootCmd.AddCommand(savedCmd)
}
