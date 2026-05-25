package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tomharris/spy/internal/resolve"
	"github.com/tomharris/spy/internal/slack"
)

// starsListResponse mirrors the stars.list payload. Items can be of
// type "message", "channel", "im", or "file" — only message items
// have a populated `message` sub-object.
type starsListResponse struct {
	slack.BaseResponse
	Items []starItem `json:"items"`
}

type starItem struct {
	Type    string `json:"type"`
	Channel string `json:"channel"`
	Message struct {
		TS   string `json:"ts"`
		User string `json:"user"`
		Text string `json:"text"`
	} `json:"message"`
	File struct {
		Name string `json:"name"`
	} `json:"file"`
}

type vipUser struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

type starEntry struct {
	Type        string `json:"type"`
	ChannelID   string `json:"channel_id,omitempty"`
	ChannelName string `json:"channel_name,omitempty"`
	Timestamp   string `json:"ts,omitempty"`
	UserID      string `json:"user_id,omitempty"`
	UserName    string `json:"user_name,omitempty"`
	Text        string `json:"text,omitempty"`
	FileName    string `json:"file_name,omitempty"`
}

type starredResult struct {
	VIPs    []vipUser   `json:"vips"`
	Starred []starEntry `json:"starred"`
}

var starredCmd = &cobra.Command{
	Use:     "starred",
	Aliases: []string{"star"},
	Short:   "List VIP users and starred items",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, r, err := newClientResolver()
		if err != nil {
			return err
		}
		res, err := runStarred(cmd.Context(), client, r)
		if err != nil {
			return err
		}
		if flagJSON {
			return emitJSON(res)
		}
		if len(res.VIPs) > 0 {
			fmt.Println("👑 VIP Users:")
			for _, v := range res.VIPs {
				fmt.Printf("   %s (%s)\n", v.Name, v.ID)
			}
			fmt.Println()
		}
		if len(res.Starred) == 0 {
			fmt.Println("⭐ No starred items.")
			return nil
		}
		fmt.Println("⭐ Starred:")
		for _, s := range res.Starred {
			ch := s.ChannelName
			if ch == "" {
				ch = s.ChannelID
			}
			switch s.Type {
			case "message":
				fmt.Printf("   #%s — %s: %s\n", ch, s.UserName, truncateRunes(s.Text, 100))
			case "channel":
				fmt.Printf("   #%s\n", ch)
			case "im":
				fmt.Printf("   💬 %s\n", ch)
			case "file":
				fmt.Printf("   📎 %s\n", s.FileName)
			}
		}
		return nil
	},
}

func runStarred(ctx context.Context, client *slack.Client, r *resolve.Resolver) (*starredResult, error) {
	var prefs usersPrefsResponse
	if err := client.Call(ctx, "users.prefs.get", nil, &prefs); err != nil {
		return nil, err
	}
	vips := make([]vipUser, 0)
	for _, id := range splitCSV(prefs.Prefs.VIPUsers) {
		vips = append(vips, vipUser{ID: id, Name: r.UserName(ctx, id)})
	}

	channels, err := r.Channels(ctx)
	if err != nil {
		return nil, err
	}
	chMap := make(map[string]resolve.Channel, len(channels))
	for _, ch := range channels {
		chMap[ch.ID] = ch
	}

	var stars starsListResponse
	if err := client.Call(ctx, "stars.list", map[string]any{"count": 50}, &stars); err != nil {
		return nil, err
	}
	out := make([]starEntry, 0, len(stars.Items))
	for _, item := range stars.Items {
		entry := starEntry{
			Type:        item.Type,
			ChannelID:   item.Channel,
			ChannelName: channelLabel(chMap, item.Channel, r, ctx),
		}
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
	return &starredResult{VIPs: vips, Starred: out}, nil
}

// channelLabel returns the channel's name, or a "DM:<user>" tag for IMs,
// or the raw ID if we don't have it cached.
func channelLabel(chMap map[string]resolve.Channel, id string, r *resolve.Resolver, ctx context.Context) string {
	ch, ok := chMap[id]
	if !ok {
		return id
	}
	if ch.Name != "" {
		return ch.Name
	}
	if ch.User != "" {
		return "DM:" + r.UserName(ctx, ch.User)
	}
	return id
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// truncateRunes cuts a string at n runes so we don't bisect a multi-byte
// codepoint mid-emoji.
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func init() {
	rootCmd.AddCommand(starredCmd)
}
