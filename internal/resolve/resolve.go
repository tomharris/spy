// Package resolve looks up users and channels by name or ID for a single
// workspace. Results are cached on disk (per-workspace, 5-minute TTL) so
// repeated CLI invocations don't re-paginate users.list / conversations.list.
package resolve

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/tomharris/spy/internal/auth"
	"github.com/tomharris/spy/internal/cache"
	"github.com/tomharris/spy/internal/slack"
)

const cacheTTL = 5 * time.Minute

// Resolver is bound to a single workspace via the slack.Client it wraps.
// Safe for sequential use by one command; not designed for concurrent reads
// of the same uncached entity.
type Resolver struct {
	client *slack.Client
	source *auth.Source
}

func New(client *slack.Client, source *auth.Source) *Resolver {
	return &Resolver{client: client, source: source}
}

func (r *Resolver) wsDir() string {
	return r.source.WorkspaceDir(r.client.Workspace().TeamID)
}

// ── Users ────────────────────────────────────────────────

// User is the trimmed-down user record we cache. Drops bot/deleted users
// and the heavy `profile` blob we don't render.
type User struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	RealName    string `json:"real_name,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	StatusText  string `json:"status_text,omitempty"`
}

// FriendlyName picks the best human-readable label for a user.
func (u *User) FriendlyName() string {
	switch {
	case u.RealName != "":
		return u.RealName
	case u.DisplayName != "":
		return u.DisplayName
	default:
		return u.Name
	}
}

type usersListResponse struct {
	slack.BaseResponse
	Members []rawUser `json:"members"`
}

type rawUser struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	RealName string `json:"real_name"`
	Deleted  bool   `json:"deleted"`
	IsBot    bool   `json:"is_bot"`
	Profile  struct {
		DisplayName string `json:"display_name"`
		RealName    string `json:"real_name"`
		StatusText  string `json:"status_text"`
	} `json:"profile"`
}

func (r *Resolver) usersCachePath() string {
	return filepath.Join(r.wsDir(), "users.json")
}

// Users returns the workspace's active users, hitting the disk cache when
// fresh. Bots and deleted users are filtered out.
func (r *Resolver) Users(ctx context.Context) ([]User, error) {
	path := r.usersCachePath()
	if cached, ok := cache.Load[[]User](path, cacheTTL); ok {
		return cached, nil
	}

	members, err := slack.Paginate(ctx, r.client, "users.list", nil,
		func() *usersListResponse { return &usersListResponse{} },
		func(p *usersListResponse) []rawUser { return p.Members },
	)
	if err != nil {
		return nil, err
	}

	users := make([]User, 0, len(members))
	for _, m := range members {
		if m.Deleted {
			continue
		}
		realName := m.RealName
		if realName == "" {
			realName = m.Profile.RealName
		}
		users = append(users, User{
			ID:          m.ID,
			Name:        m.Name,
			RealName:    realName,
			DisplayName: m.Profile.DisplayName,
			StatusText:  m.Profile.StatusText,
		})
	}
	_ = cache.Save(path, users)
	return users, nil
}

// UserName resolves a user ID to a friendly name. Returns the ID itself if
// not found (matches the JS behavior — better than crashing on a stranger).
func (r *Resolver) UserName(ctx context.Context, id string) string {
	if id == "" {
		return ""
	}
	users, err := r.Users(ctx)
	if err != nil {
		return id
	}
	for i := range users {
		if users[i].ID == id {
			return users[i].FriendlyName()
		}
	}
	return id
}

// UserByHandle resolves "@username" or "username" (case-insensitive) to a
// User, trying name → display_name → real_name in order.
func (r *Resolver) UserByHandle(ctx context.Context, handle string) (*User, error) {
	want := strings.ToLower(strings.TrimPrefix(handle, "@"))
	if want == "" {
		return nil, fmt.Errorf("empty user handle")
	}
	users, err := r.Users(ctx)
	if err != nil {
		return nil, err
	}
	for i := range users {
		u := &users[i]
		if strings.EqualFold(u.Name, want) ||
			strings.EqualFold(u.DisplayName, want) ||
			strings.EqualFold(u.RealName, want) {
			return u, nil
		}
	}
	return nil, fmt.Errorf("user not found: %s", handle)
}

// ── Channels ─────────────────────────────────────────────

// Channel is the trimmed-down channel record we cache.
type Channel struct {
	ID         string `json:"id"`
	Name       string `json:"name,omitempty"`
	IsPrivate  bool   `json:"is_private,omitempty"`
	IsIM       bool   `json:"is_im,omitempty"`
	IsMPIM     bool   `json:"is_mpim,omitempty"`
	IsArchived bool   `json:"is_archived,omitempty"`
	NumMembers int    `json:"num_members,omitempty"`
	User       string `json:"user,omitempty"` // for IM: the other user's ID
}

type convListResponse struct {
	slack.BaseResponse
	Channels []Channel `json:"channels"`
}

func (r *Resolver) channelsCachePath() string {
	return filepath.Join(r.wsDir(), "channels.json")
}

// Channels returns every conversation visible to the user: public, private,
// IMs, and MPIMs (excluding archived). Cached per-workspace with the
// default TTL.
func (r *Resolver) Channels(ctx context.Context) ([]Channel, error) {
	path := r.channelsCachePath()
	if cached, ok := cache.Load[[]Channel](path, cacheTTL); ok {
		return cached, nil
	}

	channels, err := slack.Paginate(ctx, r.client, "conversations.list",
		map[string]any{
			"types":            "public_channel,private_channel,mpim,im",
			"exclude_archived": true,
		},
		func() *convListResponse { return &convListResponse{} },
		func(p *convListResponse) []Channel { return p.Channels },
	)
	if err != nil {
		return nil, err
	}
	_ = cache.Save(path, channels)
	return channels, nil
}

// ResolveChannel turns a user-supplied reference into a Slack channel ID.
//
// Accepted forms:
//   - "C...", "D...", "G..." — channel/DM/group ID (returned as-is)
//   - "U..." — user ID, opens a DM and returns its ID
//   - "@name" or "name" matching a known user — opens a DM
//   - "name" matching a known channel — returns its ID
//   - "#name" — same as "name"
//
// DM opens are routed through conversations.open so we get a stable channel ID.
func (r *Resolver) ResolveChannel(ctx context.Context, ref string) (string, error) {
	if ref == "" {
		return "", fmt.Errorf("empty channel reference")
	}

	switch ref[0] {
	case 'C', 'D', 'G':
		return ref, nil
	case 'U':
		return r.openDM(ctx, ref)
	}

	if strings.HasPrefix(ref, "@") {
		u, err := r.UserByHandle(ctx, ref)
		if err != nil {
			return "", err
		}
		return r.openDM(ctx, u.ID)
	}

	name := strings.TrimPrefix(ref, "#")
	channels, err := r.Channels(ctx)
	if err != nil {
		return "", err
	}
	for i := range channels {
		if channels[i].Name == name {
			return channels[i].ID, nil
		}
	}

	// Fallback: maybe it's a username without @
	if u, err := r.UserByHandle(ctx, name); err == nil {
		return r.openDM(ctx, u.ID)
	}

	return "", fmt.Errorf("channel or user not found: %s", ref)
}

type convOpenResponse struct {
	slack.BaseResponse
	Channel struct {
		ID string `json:"id"`
	} `json:"channel"`
}

func (r *Resolver) openDM(ctx context.Context, userID string) (string, error) {
	var out convOpenResponse
	if err := r.client.Call(ctx, "conversations.open", map[string]any{"users": userID}, &out); err != nil {
		return "", fmt.Errorf("open dm with %s: %w", userID, err)
	}
	return out.Channel.ID, nil
}

// InvalidateAll wipes the on-disk caches for this resolver's workspace.
// Useful when --refresh is set.
func (r *Resolver) InvalidateAll() {
	_ = cache.Invalidate(r.usersCachePath())
	_ = cache.Invalidate(r.channelsCachePath())
}
