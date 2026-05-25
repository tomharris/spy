package main

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"github.com/tomharris/spy/internal/resolve"
	"github.com/tomharris/spy/internal/slack"
)

// mcpVersion is reported to MCP clients in the server handshake. Bumped
// independently from any user-visible release; clients use it for cache
// invalidation of tool/schema metadata.
const mcpVersion = "0.1.0"

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Run as an MCP (Model Context Protocol) server over stdio",
	Long: `Run as an MCP server, exposing every read/write spy command as a tool.

The server resolves a single target workspace at startup using the same rules
as the CLI (--workspace > SPY_WORKSPACE > configured default > only signed-in
workspace) and keeps that binding for the lifetime of the process. Use one
MCP server per workspace; launch multiple processes with different
--workspace flags if you need more than one.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		client, r, err := newClientResolver()
		if err != nil {
			return err
		}
		server := buildMCPServer(client, r)
		return server.Run(cmd.Context(), &mcp.StdioTransport{})
	},
}

// buildMCPServer constructs the MCP server and registers every tool. Split
// out so tests (and a future in-memory transport debug command) can build
// the same server without touching stdio.
func buildMCPServer(client *slack.Client, r *resolve.Resolver) *mcp.Server {
	ws := client.Workspace()
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "spy",
		Version: mcpVersion,
		Title:   fmt.Sprintf("spy — Slack (%s)", ws.TeamDomain),
	}, nil)

	registerReadTools(server, client, r)
	registerWriteTools(server, client, r)
	return server
}

// ── read-side tools ─────────────────────────────────────────

type emptyArgs struct{}

type readArgs struct {
	Channel       string `json:"channel" jsonschema:"channel name (with or without #), DM @handle, user ID (U…), or channel/DM ID (C…/D…/G…)"`
	Count         int    `json:"count,omitempty" jsonschema:"max messages to return (default 20)"`
	From          string `json:"from,omitempty" jsonschema:"include messages from this date onward (YYYY-MM-DD)"`
	To            string `json:"to,omitempty" jsonschema:"include messages up to this date (YYYY-MM-DD)"`
	ExpandThreads bool   `json:"expand_threads,omitempty" jsonschema:"if true, also fetch replies for every threaded message"`
}

type threadArgs struct {
	Channel string `json:"channel" jsonschema:"channel reference (see read tool)"`
	TS      string `json:"ts" jsonschema:"thread parent timestamp (e.g. 1779733418.143004)"`
	Count   int    `json:"count,omitempty" jsonschema:"max messages to return (default 50)"`
}

type searchArgs struct {
	Query string `json:"query" jsonschema:"Slack search query — same syntax as the Slack UI search bar"`
	Count int    `json:"count,omitempty" jsonschema:"max matches to return (default 20)"`
}

type channelRefArgs struct {
	Channel string `json:"channel" jsonschema:"channel reference (see read tool)"`
}

type savedArgs struct {
	Count            int  `json:"count,omitempty" jsonschema:"max items to return (default 20)"`
	IncludeCompleted bool `json:"include_completed,omitempty" jsonschema:"include items marked completed"`
}

func registerReadTools(server *mcp.Server, client *slack.Client, r *resolve.Resolver) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "auth",
		Description: "Verify Slack auth and return the signed-in user/team info for the bound workspace.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ emptyArgs) (*mcp.CallToolResult, *authTestResponse, error) {
		res, err := runAuth(ctx, client)
		return nil, res, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "channels",
		Description: "List every public and private channel in the workspace (excludes DMs/group DMs).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ emptyArgs) (*mcp.CallToolResult, *channelsResult, error) {
		res, err := runChannels(ctx, r)
		return nil, res, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "users",
		Description: "List every user in the workspace (cached for 5 minutes).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ emptyArgs) (*mcp.CallToolResult, *usersResult, error) {
		res, err := runUsers(ctx, r)
		return nil, res, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "dms",
		Description: "List active direct-message conversations with resolved counterparty names.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ emptyArgs) (*mcp.CallToolResult, *dmsResult, error) {
		res, err := runDMs(ctx, r)
		return nil, res, err
	})

	// `read` and `thread` use Out=any: the message struct references itself
	// (Replies []message for --threads), which trips the jsonschema-go cycle
	// detector. Out=any skips output-schema generation; the SDK still emits
	// the result as JSON content, so clients see the same payload.
	mcp.AddTool(server, &mcp.Tool{
		Name:        "read",
		Description: "Read recent messages from a channel or DM. Returns messages oldest-to-newest.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a readArgs) (*mcp.CallToolResult, any, error) {
		count := a.Count
		if count <= 0 {
			count = 20
		}
		oldest, err := parseDate(a.From, "from")
		if err != nil {
			return nil, nil, err
		}
		latest, err := parseDate(a.To, "to")
		if err != nil {
			return nil, nil, err
		}
		res, err := runRead(ctx, client, r, a.Channel, count, oldest, latest, a.ExpandThreads)
		return nil, res, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "thread",
		Description: "Read every message in a thread (parent + replies).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a threadArgs) (*mcp.CallToolResult, any, error) {
		count := a.Count
		if count <= 0 {
			count = 50
		}
		res, err := runThread(ctx, client, r, a.Channel, a.TS, count)
		return nil, res, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "search",
		Description: "Search messages workspace-wide. Query uses the same syntax as the Slack search bar.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a searchArgs) (*mcp.CallToolResult, *searchResult, error) {
		count := a.Count
		if count <= 0 {
			count = 20
		}
		res, err := runSearch(ctx, client, r, a.Query, count)
		return nil, res, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "pins",
		Description: "List pinned items (messages + files) in a channel or DM.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a channelRefArgs) (*mcp.CallToolResult, *pinsResult, error) {
		res, err := runPins(ctx, client, r, a.Channel)
		return nil, res, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "activity",
		Description: "Show unread and mention counts across every channel/DM. Includes muted channels.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ emptyArgs) (*mcp.CallToolResult, *activityResult, error) {
		res, err := runActivity(ctx, client, r, false)
		return nil, res, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "unread",
		Description: "Show only conversations with unreads or mentions. Excludes muted channels.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ emptyArgs) (*mcp.CallToolResult, *activityResult, error) {
		res, err := runActivity(ctx, client, r, true)
		return nil, res, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "starred",
		Description: "List VIP users and starred items (messages, channels, DMs, files).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ emptyArgs) (*mcp.CallToolResult, *starredResult, error) {
		res, err := runStarred(ctx, client, r)
		return nil, res, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "saved",
		Description: "List Saved for Later items, including the original message text for each.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a savedArgs) (*mcp.CallToolResult, *savedResult, error) {
		count := a.Count
		if count <= 0 {
			count = 20
		}
		res, err := runSaved(ctx, client, r, count, a.IncludeCompleted)
		return nil, res, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "drafts_list",
		Description: "List the user's active drafts (across channels, DMs, and thread replies).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ emptyArgs) (*mcp.CallToolResult, *draftsListResult, error) {
		res, err := runDraftsList(ctx, client)
		return nil, res, err
	})
}

// ── write-side tools ────────────────────────────────────────

type sendArgs struct {
	Channel string `json:"channel" jsonschema:"channel reference (see read tool)"`
	Text    string `json:"text" jsonschema:"message text — plain or Slack mrkdwn"`
}

type reactArgs struct {
	Channel string `json:"channel" jsonschema:"channel reference (see read tool)"`
	TS      string `json:"ts" jsonschema:"timestamp of the message to react to"`
	Emoji   string `json:"emoji" jsonschema:"emoji name with or without surrounding colons (e.g. 'eyes' or ':eyes:')"`
}

type draftChannelArgs struct {
	Channel string `json:"channel" jsonschema:"channel reference (see read tool)"`
	Text    string `json:"text" jsonschema:"draft body"`
}

type draftThreadArgs struct {
	Channel string `json:"channel" jsonschema:"channel reference (see read tool)"`
	TS      string `json:"ts" jsonschema:"thread parent timestamp"`
	Text    string `json:"text" jsonschema:"draft body"`
}

type draftUserArgs struct {
	User string `json:"user" jsonschema:"user ID (U…), @handle, or bare handle/display name"`
	Text string `json:"text" jsonschema:"draft body"`
}

type draftDropArgs struct {
	DraftID string `json:"draft_id" jsonschema:"id of the draft to delete (from drafts_list)"`
}

type draftDropResult struct {
	OK      bool   `json:"ok"`
	DraftID string `json:"draft_id"`
}

func registerWriteTools(server *mcp.Server, client *slack.Client, r *resolve.Resolver) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "send",
		Description: "Send a message to a channel or DM. Returns the posted message's timestamp.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a sendArgs) (*mcp.CallToolResult, *sendResult, error) {
		res, err := runSend(ctx, client, r, a.Channel, a.Text)
		return nil, res, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "react",
		Description: "Add an emoji reaction to a message.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a reactArgs) (*mcp.CallToolResult, *reactResult, error) {
		res, err := runReact(ctx, client, r, a.Channel, a.TS, a.Emoji)
		return nil, res, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "draft_channel",
		Description: "Save a draft message in a channel/DM — appears in the Slack UI, does not send.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a draftChannelArgs) (*mcp.CallToolResult, *draftCreatedResult, error) {
		res, err := runDraftChannel(ctx, client, r, a.Channel, a.Text)
		return nil, res, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "draft_thread",
		Description: "Save a draft reply on a thread.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a draftThreadArgs) (*mcp.CallToolResult, *draftCreatedResult, error) {
		res, err := runDraftThread(ctx, client, r, a.Channel, a.TS, a.Text)
		return nil, res, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "draft_user",
		Description: "Save a draft DM to a user (opens the DM if needed).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a draftUserArgs) (*mcp.CallToolResult, *draftCreatedResult, error) {
		res, err := runDraftUser(ctx, client, r, a.User, a.Text)
		return nil, res, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "draft_drop",
		Description: "Delete a draft by id (from drafts_list).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a draftDropArgs) (*mcp.CallToolResult, *draftDropResult, error) {
		if err := runDraftDrop(ctx, client, a.DraftID); err != nil {
			return nil, nil, err
		}
		return nil, &draftDropResult{OK: true, DraftID: a.DraftID}, nil
	})
}

func init() {
	rootCmd.AddCommand(mcpCmd)
}
