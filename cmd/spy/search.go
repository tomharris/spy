package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tomharris/spy/internal/resolve"
	"github.com/tomharris/spy/internal/slack"
)

// searchMessagesResponse is the shape returned by search.messages. The
// useful data hangs off `messages.matches` — note the nested envelope,
// which is why we can't reuse historyResponse.
type searchMessagesResponse struct {
	slack.BaseResponse
	Messages struct {
		Total   int           `json:"total"`
		Matches []searchMatch `json:"matches"`
	} `json:"messages"`
}

type searchMatch struct {
	TS      string `json:"ts"`
	User    string `json:"user"`
	Text    string `json:"text"`
	Channel struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"channel"`
	Permalink string `json:"permalink"`
}

type searchHit struct {
	Timestamp   string `json:"ts"`
	UserID      string `json:"user_id,omitempty"`
	UserName    string `json:"user_name,omitempty"`
	ChannelID   string `json:"channel_id"`
	ChannelName string `json:"channel_name,omitempty"`
	Text        string `json:"text"`
	Permalink   string `json:"permalink,omitempty"`
}

type searchResult struct {
	Query string      `json:"query"`
	Total int         `json:"total"`
	Hits  []searchHit `json:"hits"`
}

var searchCmd = &cobra.Command{
	Use:   "search <query...> [count]",
	Short: "Search messages across the workspace",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		query, count := parseSearchArgs(args)
		client, r, err := newClientResolver()
		if err != nil {
			return err
		}
		res, err := runSearch(cmd.Context(), client, r, query, count)
		if err != nil {
			return err
		}
		if flagJSON {
			return emitJSON(res)
		}
		fmt.Printf("Found %d results\n\n", res.Total)
		for _, h := range res.Hits {
			ch := h.ChannelName
			if ch == "" {
				ch = h.ChannelID
			}
			fmt.Printf("[%s] #%s — %s:\n", formatTS(h.Timestamp), ch, h.UserName)
			fmt.Printf("  %s\n\n", h.Text)
		}
		return nil
	},
}

// parseSearchArgs treats a trailing positive integer as the count, the
// rest as the query string. E.g. `spy search build failed 10`
// is "build failed", count=10.
func parseSearchArgs(args []string) (query string, count int) {
	count = 20
	last := args[len(args)-1]
	if n, err := strconv.Atoi(last); err == nil && n > 0 && len(args) > 1 {
		return strings.Join(args[:len(args)-1], " "), n
	}
	return strings.Join(args, " "), count
}

func runSearch(ctx context.Context, client *slack.Client, r *resolve.Resolver, query string, count int) (*searchResult, error) {
	var res searchMessagesResponse
	if err := client.Call(ctx, "search.messages", map[string]any{
		"query": query,
		"count": count,
	}, &res); err != nil {
		return nil, err
	}
	hits := make([]searchHit, 0, len(res.Messages.Matches))
	for _, m := range res.Messages.Matches {
		hits = append(hits, searchHit{
			Timestamp:   m.TS,
			UserID:      m.User,
			UserName:    r.UserName(ctx, m.User),
			ChannelID:   m.Channel.ID,
			ChannelName: m.Channel.Name,
			Text:        m.Text,
			Permalink:   m.Permalink,
		})
	}
	return &searchResult{Query: query, Total: res.Messages.Total, Hits: hits}, nil
}

func init() {
	rootCmd.AddCommand(searchCmd)
}
