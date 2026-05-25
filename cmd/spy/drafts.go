package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/tomharris/spy/internal/resolve"
	"github.com/tomharris/spy/internal/slack"
)

// ── drafts.list / .create / .delete shapes ──────────────────

type draftsListResponse struct {
	slack.BaseResponse
	Drafts []rawDraft `json:"drafts"`
}

type rawDraft struct {
	ID            string           `json:"id"`
	LastUpdatedTS string           `json:"last_updated_ts"` // Slack epoch-seconds string with sub-second precision
	DateCreated   int64            `json:"date_created"`
	IsDeleted     bool             `json:"is_deleted"`
	IsSent        bool             `json:"is_sent"`
	Destinations  []draftDest      `json:"destinations"`
	Blocks        []map[string]any `json:"blocks"`
}

type draftDest struct {
	ChannelID string `json:"channel_id"`
	ThreadTS  string `json:"thread_ts,omitempty"`
}

type draftsCreateResponse struct {
	slack.BaseResponse
	Draft struct {
		ID string `json:"id"`
	} `json:"draft"`
}

// ── runDraft* return shapes ──────────────────────────────────

type draftCreatedResult struct {
	DraftID   string `json:"draft_id"`
	ChannelID string `json:"channel_id"`
	ThreadTS  string `json:"thread_ts,omitempty"`
	Text      string `json:"text"`
}

type draftEntry struct {
	ID          string `json:"id"`
	ChannelID   string `json:"channel_id"`
	ThreadTS    string `json:"thread_ts,omitempty"`
	Text        string `json:"text"`
	AgeSeconds  int64  `json:"age_seconds"`
}

type draftsListResult struct {
	Drafts []draftEntry `json:"drafts"`
}

// ── drafts (list) ────────────────────────────────────────────

var draftsCmd = &cobra.Command{
	Use:   "drafts",
	Short: "List active drafts",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, _, err := newClientResolver()
		if err != nil {
			return err
		}
		res, err := runDraftsList(cmd.Context(), client)
		if err != nil {
			return err
		}
		if flagJSON {
			return emitJSON(res)
		}
		if len(res.Drafts) == 0 {
			fmt.Println("No active drafts.")
			return nil
		}
		for _, d := range res.Drafts {
			thread := ""
			if d.ThreadTS != "" {
				thread = fmt.Sprintf(" (thread %s)", d.ThreadTS)
			}
			fmt.Printf("%s  → %s%s  (%s)\n", d.ID, d.ChannelID, thread, humanAge(d.AgeSeconds))
			fmt.Printf("   %s\n\n", truncateRunes(d.Text, 120))
		}
		return nil
	},
}

func runDraftsList(ctx context.Context, client *slack.Client) (*draftsListResult, error) {
	var list draftsListResponse
	if err := client.Call(ctx, "drafts.list", nil, &list); err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	out := &draftsListResult{Drafts: []draftEntry{}}
	for _, d := range list.Drafts {
		if d.IsDeleted || d.IsSent {
			continue
		}
		var dest draftDest
		if len(d.Destinations) > 0 {
			dest = d.Destinations[0]
		}
		out.Drafts = append(out.Drafts, draftEntry{
			ID:         d.ID,
			ChannelID:  dest.ChannelID,
			ThreadTS:   dest.ThreadTS,
			Text:       extractDraftText(d.Blocks),
			AgeSeconds: now - d.DateCreated,
		})
	}
	return out, nil
}

// ── draft (create / thread / user / drop) ───────────────────

var draftCmd = &cobra.Command{
	Use:   "draft <channel> <message...>",
	Short: "Save a draft in the Slack UI",
	Args:  cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, r, err := newClientResolver()
		if err != nil {
			return err
		}
		text := strings.Join(args[1:], " ")
		res, err := runDraftChannel(cmd.Context(), client, r, args[0], text)
		if err != nil {
			return err
		}
		return emitDraftCreated(res, fmt.Sprintf("#%s", args[0]))
	},
}

var draftThreadCmd = &cobra.Command{
	Use:   "thread <channel> <ts> <message...>",
	Short: "Save a draft reply in a thread",
	Args:  cobra.MinimumNArgs(3),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, r, err := newClientResolver()
		if err != nil {
			return err
		}
		text := strings.Join(args[2:], " ")
		res, err := runDraftThread(cmd.Context(), client, r, args[0], args[1], text)
		if err != nil {
			return err
		}
		return emitDraftCreated(res, fmt.Sprintf("#%s thread %s", args[0], args[1]))
	},
}

var draftUserCmd = &cobra.Command{
	Use:   "user <user_id|@handle> <message...>",
	Short: "Save a draft DM to a user",
	Args:  cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, r, err := newClientResolver()
		if err != nil {
			return err
		}
		text := strings.Join(args[1:], " ")
		res, err := runDraftUser(cmd.Context(), client, r, args[0], text)
		if err != nil {
			return err
		}
		return emitDraftCreated(res, fmt.Sprintf("DM %s", args[0]))
	},
}

var draftDropCmd = &cobra.Command{
	Use:   "drop <draft_id>",
	Short: "Delete a draft",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, _, err := newClientResolver()
		if err != nil {
			return err
		}
		if err := runDraftDrop(cmd.Context(), client, args[0]); err != nil {
			return err
		}
		if flagJSON {
			return emitJSON(map[string]any{"ok": true, "draft_id": args[0]})
		}
		fmt.Printf("🗑  Draft %s deleted.\n", args[0])
		return nil
	},
}

func emitDraftCreated(res *draftCreatedResult, label string) error {
	if flagJSON {
		return emitJSON(res)
	}
	fmt.Printf("draft saved → %s  (%s)\n", label, res.DraftID)
	fmt.Println("   Check Slack — draft icon should appear.")
	return nil
}

// ── runDraft* implementations ───────────────────────────────

func runDraftChannel(ctx context.Context, client *slack.Client, r *resolve.Resolver, channelRef, text string) (*draftCreatedResult, error) {
	channelID, err := r.ResolveChannel(ctx, channelRef)
	if err != nil {
		return nil, err
	}
	return createDraft(ctx, client, channelID, "", text)
}

func runDraftThread(ctx context.Context, client *slack.Client, r *resolve.Resolver, channelRef, ts, text string) (*draftCreatedResult, error) {
	channelID, err := r.ResolveChannel(ctx, channelRef)
	if err != nil {
		return nil, err
	}
	return createDraft(ctx, client, channelID, ts, text)
}

func runDraftUser(ctx context.Context, client *slack.Client, r *resolve.Resolver, userRef, text string) (*draftCreatedResult, error) {
	// ResolveChannel already handles U…, @handle, and bare handle — and
	// it opens the DM to return a stable channel ID. Exactly what we need.
	channelID, err := r.ResolveChannel(ctx, userRef)
	if err != nil {
		return nil, err
	}
	return createDraft(ctx, client, channelID, "", text)
}

// runDraftDrop deletes a draft.
//
// Slack uses optimistic concurrency on drafts.delete. Passing back the
// last_updated_ts value from drafts.list isn't reliable — the user's
// running Slack desktop app keeps bumping the server-side ts after our
// API call returns, racing us. Sending the current wall time as a float
// wins the check unconditionally, since it's always >= whatever the
// server has.
func runDraftDrop(ctx context.Context, client *slack.Client, draftID string) error {
	var list draftsListResponse
	if err := client.Call(ctx, "drafts.list", nil, &list); err != nil {
		return err
	}
	found := false
	for _, d := range list.Drafts {
		if d.ID == draftID {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("draft %s not found", draftID)
	}
	var del slack.BaseResponse
	return client.Call(ctx, "drafts.delete", map[string]any{
		"draft_id":               draftID,
		"client_last_updated_ts": float64(time.Now().UnixMilli()) / 1000.0,
	}, &del)
}

func createDraft(ctx context.Context, client *slack.Client, channelID, threadTS, text string) (*draftCreatedResult, error) {
	dest := map[string]any{"channel_id": channelID}
	if threadTS != "" {
		dest["thread_ts"] = threadTS
		dest["broadcast"] = false
	}
	params := map[string]any{
		"client_msg_id":    uuid.NewString(),
		"is_from_composer": false,
		"file_ids":         []string{},
		"destinations":     []any{dest},
		"blocks": []any{
			map[string]any{
				"type": "rich_text",
				"elements": []any{
					map[string]any{
						"type": "rich_text_section",
						"elements": []any{
							map[string]any{"type": "text", "text": text},
						},
					},
				},
			},
		},
	}
	var res draftsCreateResponse
	if err := client.Call(ctx, "drafts.create", params, &res); err != nil {
		return nil, err
	}
	return &draftCreatedResult{
		DraftID:   res.Draft.ID,
		ChannelID: channelID,
		ThreadTS:  threadTS,
		Text:      text,
	}, nil
}

// ── helpers ─────────────────────────────────────────────────

// extractDraftText flattens a rich_text block tree back to plain text.
// Drafts use the same nested structure as posted messages.
func extractDraftText(blocks []map[string]any) string {
	if len(blocks) == 0 {
		return "(empty)"
	}
	var parts []string
	for _, b := range blocks {
		for _, el := range asMapList(b["elements"]) {
			for _, item := range asMapList(el["elements"]) {
				switch item["type"] {
				case "text":
					if s, ok := item["text"].(string); ok {
						parts = append(parts, s)
					}
				case "emoji":
					if s, ok := item["name"].(string); ok {
						parts = append(parts, ":"+s+":")
					}
				case "link":
					if s, ok := item["url"].(string); ok {
						parts = append(parts, s)
					}
				}
			}
		}
	}
	if len(parts) == 0 {
		return "(empty)"
	}
	return strings.Join(parts, "")
}

func asMapList(v any) []map[string]any {
	raw, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func humanAge(secs int64) string {
	if secs < 60 {
		return "just now"
	}
	mins := secs / 60
	if mins < 60 {
		return fmt.Sprintf("%dm ago", mins)
	}
	hrs := mins / 60
	if hrs < 24 {
		return fmt.Sprintf("%dh ago", hrs)
	}
	return fmt.Sprintf("%dd ago", hrs/24)
}

func init() {
	draftCmd.AddCommand(draftThreadCmd)
	draftCmd.AddCommand(draftUserCmd)
	draftCmd.AddCommand(draftDropCmd)
	rootCmd.AddCommand(draftCmd)
	rootCmd.AddCommand(draftsCmd)
}
