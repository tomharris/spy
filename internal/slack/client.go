package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/tomharris/spy/internal/auth"
)

const baseURL = "https://slack.com/api"

// Client is an authenticated Slack Web API client bound to a single
// workspace. Safe for concurrent use.
//
// On `invalid_auth` the client re-extracts workspaces from Slack
// (auth.ListWorkspaces(src, true)) and looks for the same team_id —
// covering the case where the user signed out and back in, rotating the
// xoxc token but keeping the team identity. Rate-limited responses
// (HTTP 429) are honored via Retry-After.
type Client struct {
	httpClient *http.Client
	src        *auth.Source

	mu        sync.RWMutex
	workspace *auth.Workspace
	cookie    string
}

// NewClient creates a Slack client for the given workspace. The shared
// account cookie is decrypted lazily here so callers don't pay for it
// just to construct a client.
func NewClient(src *auth.Source, workspace *auth.Workspace) (*Client, error) {
	if workspace == nil {
		return nil, fmt.Errorf("workspace is required")
	}
	cookie, err := auth.SharedCookie(src)
	if err != nil {
		return nil, err
	}
	return &Client{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		src:        src,
		workspace:  workspace,
		cookie:     cookie,
	}, nil
}

// Workspace returns the workspace this client is bound to.
func (c *Client) Workspace() *auth.Workspace {
	c.mu.RLock()
	defer c.mu.RUnlock()
	ws := *c.workspace
	return &ws
}

// Call invokes method with params and decodes the response into out.
//
// out must be a pointer to a struct that embeds BaseResponse (so the client
// can read ok/error and pagination cursors). params may be nil.
func (c *Client) Call(ctx context.Context, method string, params map[string]any, out Response) error {
	return c.callWithRetry(ctx, method, params, out, true)
}

func (c *Client) callWithRetry(ctx context.Context, method string, params map[string]any, out Response, allowRefresh bool) error {
	verb, ok := MethodVerb(method)
	if !ok {
		return fmt.Errorf("unknown Slack method %q (register it in internal/slack/methods.go)", method)
	}

	req, err := c.buildRequest(ctx, verb, method, params)
	if err != nil {
		return err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		if wait, ok := parseRetryAfter(resp.Header.Get("Retry-After")); ok {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(wait):
			}
			return c.callWithRetry(ctx, method, params, out, allowRefresh)
		}
		return fmt.Errorf("%s: rate limited (HTTP 429, no Retry-After)", method)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("%s: read body: %w", method, err)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("%s: decode response: %w (body: %s)", method, err, truncate(body, 200))
	}
	if out.OK() {
		return nil
	}
	if out.SlackError() == "invalid_auth" && allowRefresh {
		if rerr := c.refreshCredentials(); rerr == nil {
			return c.callWithRetry(ctx, method, params, out, false)
		}
	}
	return fmt.Errorf("%s: %s", method, out.SlackError())
}

func (c *Client) buildRequest(ctx context.Context, verb Verb, method string, params map[string]any) (*http.Request, error) {
	target := baseURL + "/" + method
	creds := c.snapshotCreds()

	var req *http.Request
	switch verb {
	case POST:
		body := []byte("{}")
		if params != nil {
			b, err := json.Marshal(params)
			if err != nil {
				return nil, fmt.Errorf("marshal params: %w", err)
			}
			body = b
		}
		r, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		r.Header.Set("Content-Type", "application/json; charset=utf-8")
		req = r
	case GET:
		u, err := url.Parse(target)
		if err != nil {
			return nil, err
		}
		if len(params) > 0 {
			q := u.Query()
			for k, v := range params {
				if v == nil {
					continue
				}
				q.Set(k, fmt.Sprint(v))
			}
			u.RawQuery = q.Encode()
		}
		r, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, err
		}
		req = r
	default:
		return nil, fmt.Errorf("unsupported verb %q", verb)
	}

	req.Header.Set("Authorization", "Bearer "+creds.Token)
	req.Header.Set("Cookie", "d="+creds.Cookie)
	return req, nil
}

func (c *Client) snapshotCreds() *auth.Credentials {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.workspace.Credentials(c.cookie)
}

func (c *Client) refreshCredentials() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	workspaces, err := auth.ListWorkspaces(c.src, true)
	if err != nil {
		return err
	}
	for i := range workspaces {
		if workspaces[i].TeamID == c.workspace.TeamID {
			c.workspace = &workspaces[i]
			return nil
		}
	}
	return fmt.Errorf("workspace %s (%s) no longer signed in", c.workspace.TeamDomain, c.workspace.TeamID)
}

// parseRetryAfter accepts both seconds ("5") and HTTP-date forms.
// Returns (0, false) if the header is missing or unusable; clamps to a
// sane upper bound so a hostile header can't pin us forever.
func parseRetryAfter(h string) (time.Duration, bool) {
	if h == "" {
		return 0, false
	}
	if secs, err := strconv.Atoi(h); err == nil {
		if secs < 0 {
			return 0, false
		}
		if secs > 120 {
			secs = 120
		}
		return time.Duration(secs) * time.Second, true
	}
	if t, err := http.ParseTime(h); err == nil {
		d := time.Until(t)
		if d <= 0 {
			return 0, false
		}
		if d > 2*time.Minute {
			d = 2 * time.Minute
		}
		return d, true
	}
	return 0, false
}

func truncate(b []byte, max int) string {
	if len(b) <= max {
		return string(b)
	}
	return string(b[:max]) + "…"
}
