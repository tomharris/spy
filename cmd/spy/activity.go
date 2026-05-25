package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tomharris/spy/internal/resolve"
	"github.com/tomharris/spy/internal/slack"
)

// clientCountsResponse mirrors the undocumented client.counts response.
// It returns three parallel arrays (channels, mpims, ims) of unread/
// mention counts plus a separate threads summary.
type clientCountsResponse struct {
	slack.BaseResponse
	Channels []countEntry `json:"channels"`
	MPIMs    []countEntry `json:"mpims"`
	IMs      []countEntry `json:"ims"`
	Threads  struct {
		HasUnreads   bool `json:"has_unreads"`
		MentionCount int  `json:"mention_count"`
	} `json:"threads"`
}

type countEntry struct {
	ID           string `json:"id"`
	HasUnreads   bool   `json:"has_unreads"`
	MentionCount int    `json:"mention_count"`
}

// usersPrefsResponse exposes only the field we care about: muted
// channels live inside `all_notifications_prefs`, which Slack
// occasionally returns as a JSON-encoded string instead of an object.
type usersPrefsResponse struct {
	slack.BaseResponse
	Prefs struct {
		AllNotificationsPrefs json.RawMessage `json:"all_notifications_prefs"`
		VIPUsers              string          `json:"vip_users"`
	} `json:"prefs"`
}

type activityItem struct {
	ID           string `json:"id"`
	Type         string `json:"type"` // "channel", "group", "dm"
	Name         string `json:"name,omitempty"`
	HasUnreads   bool   `json:"has_unreads"`
	MentionCount int    `json:"mention_count,omitempty"`
	Muted        bool   `json:"muted,omitempty"`
}

type activityResult struct {
	Threads struct {
		HasUnreads   bool `json:"has_unreads"`
		MentionCount int  `json:"mention_count"`
	} `json:"threads"`
	Items []activityItem `json:"items"`
}

var activityCmd = &cobra.Command{
	Use:     "activity",
	Aliases: []string{"a"},
	Short:   "Show unread + mention counts across channels and DMs",
	RunE:    runActivityCmd(false),
}

var unreadCmd = &cobra.Command{
	Use:     "unread",
	Aliases: []string{"ur"},
	Short:   "Show conversations with unreads (excludes muted)",
	RunE:    runActivityCmd(true),
}

func runActivityCmd(unreadOnly bool) func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, args []string) error {
		client, r, err := newClientResolver()
		if err != nil {
			return err
		}
		res, err := runActivity(cmd.Context(), client, r, unreadOnly)
		if err != nil {
			return err
		}
		if flagJSON {
			return emitJSON(res)
		}
		if res.Threads.HasUnreads || res.Threads.MentionCount > 0 {
			fmt.Printf("🧵 Threads — %d mentions, unreads: %v\n\n",
				res.Threads.MentionCount, res.Threads.HasUnreads)
		}
		if len(res.Items) == 0 {
			if unreadOnly {
				fmt.Println("No unreads! 🎉")
			} else {
				fmt.Println("No activity.")
			}
			return nil
		}
		for _, it := range res.Items {
			prefix := "#"
			switch it.Type {
			case "dm":
				prefix = "💬"
			case "group":
				prefix = "👥"
			}
			mentions := ""
			if it.MentionCount > 0 {
				mentions = fmt.Sprintf(" (%d mentions)", it.MentionCount)
			}
			unread := ""
			if it.HasUnreads {
				unread = " •"
			}
			muted := ""
			if it.Muted {
				muted = " 🔇"
			}
			fmt.Printf("%s %s%s%s%s\n", prefix, it.Name, unread, mentions, muted)
		}
		return nil
	}
}

func runActivity(ctx context.Context, client *slack.Client, r *resolve.Resolver, unreadOnly bool) (*activityResult, error) {
	var counts clientCountsResponse
	if err := client.Call(ctx, "client.counts", nil, &counts); err != nil {
		return nil, err
	}
	muted, err := mutedChannels(ctx, client)
	if err != nil {
		// A missing/garbled prefs response shouldn't prevent showing activity.
		muted = map[string]bool{}
	}
	channels, err := r.Channels(ctx)
	if err != nil {
		return nil, err
	}
	chMap := make(map[string]resolve.Channel, len(channels))
	for _, ch := range channels {
		chMap[ch.ID] = ch
	}

	res := &activityResult{}
	res.Threads.HasUnreads = counts.Threads.HasUnreads
	res.Threads.MentionCount = counts.Threads.MentionCount

	add := func(entries []countEntry, typ string) {
		for _, e := range entries {
			item := activityItem{
				ID:           e.ID,
				Type:         typ,
				HasUnreads:   e.HasUnreads,
				MentionCount: e.MentionCount,
				Muted:        muted[e.ID],
			}
			if ch, ok := chMap[e.ID]; ok {
				switch {
				case ch.Name != "":
					item.Name = ch.Name
				case ch.User != "":
					item.Name = "DM:" + r.UserName(ctx, ch.User)
				default:
					item.Name = e.ID
				}
			} else {
				item.Name = e.ID
			}
			if unreadOnly && (!item.HasUnreads && item.MentionCount == 0) {
				continue
			}
			if unreadOnly && item.Muted {
				continue
			}
			res.Items = append(res.Items, item)
		}
	}
	add(counts.Channels, "channel")
	add(counts.MPIMs, "group")
	add(counts.IMs, "dm")
	return res, nil
}

// mutedChannels returns the set of channel IDs the user has muted.
// Slack sometimes returns all_notifications_prefs as a JSON string and
// sometimes as a nested object; we try the string form first since
// that's what the public clients return today.
func mutedChannels(ctx context.Context, client *slack.Client) (map[string]bool, error) {
	var prefs usersPrefsResponse
	if err := client.Call(ctx, "users.prefs.get", nil, &prefs); err != nil {
		return nil, err
	}
	if len(prefs.Prefs.AllNotificationsPrefs) == 0 {
		return map[string]bool{}, nil
	}
	type chanPref struct {
		Muted bool `json:"muted"`
	}
	type notifsShape struct {
		Channels map[string]chanPref `json:"channels"`
	}
	parsed, err := parseMaybeStringJSON[notifsShape](prefs.Prefs.AllNotificationsPrefs)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(parsed.Channels))
	for id, p := range parsed.Channels {
		if p.Muted {
			out[id] = true
		}
	}
	return out, nil
}

// parseMaybeStringJSON unmarshals a json.RawMessage that may be either a
// JSON object/array directly or a JSON-encoded string containing the
// object/array. Slack does this for nested prefs blobs.
func parseMaybeStringJSON[T any](raw json.RawMessage) (T, error) {
	var out T
	if len(raw) == 0 {
		return out, nil
	}
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return out, err
		}
		if s == "" {
			return out, nil
		}
		return out, json.Unmarshal([]byte(s), &out)
	}
	return out, json.Unmarshal(raw, &out)
}

func init() {
	rootCmd.AddCommand(activityCmd)
	rootCmd.AddCommand(unreadCmd)
}
