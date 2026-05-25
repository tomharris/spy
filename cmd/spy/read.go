package main

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/tomharris/spy/internal/resolve"
	"github.com/tomharris/spy/internal/slack"
)

var (
	readShowTS        bool
	readExpandThreads bool
	readFrom          string
	readTo            string
)

// message is the trimmed message shape we render and emit as JSON.
type message struct {
	Timestamp   string        `json:"ts"`
	UserID      string        `json:"user_id,omitempty"`
	UserName    string        `json:"user_name,omitempty"`
	Text        string        `json:"text"`
	ReplyCount  int           `json:"reply_count,omitempty"`
	ThreadTS    string        `json:"thread_ts,omitempty"`
	Files       []file        `json:"files,omitempty"`
	Replies     []message     `json:"replies,omitempty"` // populated when --threads
}

type file struct {
	Name     string `json:"name"`
	Mimetype string `json:"mimetype,omitempty"`
}

type readResult struct {
	Channel  string    `json:"channel"`
	Messages []message `json:"messages"`
}

type historyResponse struct {
	slack.BaseResponse
	Messages []rawMessage `json:"messages"`
}

type repliesResponse struct {
	slack.BaseResponse
	Messages []rawMessage `json:"messages"`
}

type rawMessage struct {
	TS         string `json:"ts"`
	User       string `json:"user"`
	Text       string `json:"text"`
	ReplyCount int    `json:"reply_count"`
	ThreadTS   string `json:"thread_ts"`
	Files      []struct {
		Name     string `json:"name"`
		Mimetype string `json:"mimetype"`
	} `json:"files"`
}

var readCmd = &cobra.Command{
	Use:     "read <channel|@user> [count]",
	Aliases: []string{"r"},
	Short:   "Read recent messages from a channel or DM",
	Args:    cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		count := 20
		if len(args) == 2 {
			n, err := strconv.Atoi(args[1])
			if err != nil || n <= 0 {
				return fmt.Errorf("count must be a positive integer, got %q", args[1])
			}
			count = n
		}

		oldest, err := parseDate(readFrom, "from")
		if err != nil {
			return err
		}
		latest, err := parseDate(readTo, "to")
		if err != nil {
			return err
		}

		client, r, err := newClientResolver()
		if err != nil {
			return err
		}

		res, err := runRead(cmd.Context(), client, r, args[0], count, oldest, latest, readExpandThreads)
		if err != nil {
			return err
		}

		if flagJSON {
			return emitJSON(res)
		}
		for _, m := range res.Messages {
			printMessage(m, "", readShowTS)
			for _, reply := range m.Replies {
				printMessage(reply, "  ↳ ", readShowTS)
			}
			fmt.Println()
		}
		return nil
	},
}

func runRead(
	ctx context.Context,
	client *slack.Client,
	r *resolve.Resolver,
	channelRef string,
	count int,
	oldest, latest string,
	expandThreads bool,
) (*readResult, error) {
	channelID, err := r.ResolveChannel(ctx, channelRef)
	if err != nil {
		return nil, err
	}

	params := map[string]any{"channel": channelID, "limit": count}
	if oldest != "" {
		params["oldest"] = oldest
	}
	if latest != "" {
		params["latest"] = latest
	}

	var hist historyResponse
	if err := client.Call(ctx, "conversations.history", params, &hist); err != nil {
		return nil, err
	}

	// Slack returns newest-first; flip so output reads top-to-bottom in time order.
	reversed := reverse(hist.Messages)

	out := make([]message, 0, len(reversed))
	for _, raw := range reversed {
		m := toMessage(ctx, r, raw)
		if expandThreads && raw.ReplyCount > 0 {
			replies, err := fetchReplies(ctx, client, r, channelID, raw.TS)
			if err == nil {
				m.Replies = replies
			}
		}
		out = append(out, m)
	}
	return &readResult{Channel: channelID, Messages: out}, nil
}

func fetchReplies(ctx context.Context, client *slack.Client, r *resolve.Resolver, channelID, ts string) ([]message, error) {
	var rep repliesResponse
	err := client.Call(ctx, "conversations.replies", map[string]any{
		"channel": channelID,
		"ts":      ts,
		"limit":   100,
	}, &rep)
	if err != nil {
		return nil, err
	}
	// First message is the parent; skip it.
	if len(rep.Messages) <= 1 {
		return nil, nil
	}
	out := make([]message, 0, len(rep.Messages)-1)
	for _, raw := range rep.Messages[1:] {
		out = append(out, toMessage(ctx, r, raw))
	}
	return out, nil
}

func toMessage(ctx context.Context, r *resolve.Resolver, raw rawMessage) message {
	m := message{
		Timestamp:  raw.TS,
		UserID:     raw.User,
		UserName:   r.UserName(ctx, raw.User),
		Text:       raw.Text,
		ReplyCount: raw.ReplyCount,
		ThreadTS:   raw.ThreadTS,
	}
	for _, f := range raw.Files {
		m.Files = append(m.Files, file{Name: f.Name, Mimetype: f.Mimetype})
	}
	return m
}

func reverse(in []rawMessage) []rawMessage {
	out := make([]rawMessage, len(in))
	for i := range in {
		out[i] = in[len(in)-1-i]
	}
	return out
}

func printMessage(m message, prefix string, showTS bool) {
	human := formatTS(m.Timestamp)
	tsTag := ""
	if showTS {
		tsTag = " ts:" + m.Timestamp
	}
	thread := ""
	if m.ReplyCount > 0 && prefix == "" {
		thread = fmt.Sprintf(" [%d replies]", m.ReplyCount)
	}
	fmt.Printf("%s[%s%s] %s%s:\n", prefix, human, tsTag, m.UserName, thread)
	bodyPrefix := "  "
	if prefix != "" {
		bodyPrefix = "      "
	}
	fmt.Printf("%s%s\n", bodyPrefix, m.Text)
	for _, f := range m.Files {
		fmt.Printf("%s📎 %s (%s)\n", bodyPrefix, f.Name, f.Mimetype)
	}
}

func formatTS(ts string) string {
	secs, err := strconv.ParseFloat(ts, 64)
	if err != nil {
		return ts
	}
	return time.Unix(int64(secs), 0).Format("2006-01-02 15:04:05")
}

// parseDate accepts YYYY-MM-DD and returns the Slack "oldest"/"latest" value
// (seconds-since-epoch as a string). Empty input is allowed and returns "".
func parseDate(s, name string) (string, error) {
	if s == "" {
		return "", nil
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return "", fmt.Errorf("--%s: expected YYYY-MM-DD, got %q", name, s)
	}
	return strconv.FormatInt(t.Unix(), 10), nil
}

func init() {
	readCmd.Flags().BoolVar(&readShowTS, "ts", false, "show raw Slack timestamps (useful to feed `spy thread`)")
	readCmd.Flags().BoolVar(&readExpandThreads, "threads", false, "auto-expand all threads")
	readCmd.Flags().StringVar(&readFrom, "from", "", "include messages from YYYY-MM-DD onward")
	readCmd.Flags().StringVar(&readTo, "to", "", "include messages up to YYYY-MM-DD")
	rootCmd.AddCommand(readCmd)
}
