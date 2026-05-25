package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Workspace is a single signed-in Slack workspace plus its current token.
//
// TeamID is the public T-prefixed id (canonical identity, used as the
// cache filename). TeamDomain is the URL subdomain (the friendly id users
// type at --workspace).
type Workspace struct {
	TeamID     string `json:"team_id"`
	TeamName   string `json:"team_name"`
	TeamDomain string `json:"team_domain"`
	UserID     string `json:"user_id"`
	UserName   string `json:"user_name"`
	URL        string `json:"url"`
	Token      string `json:"token"`
	LastSeenMS int64  `json:"last_seen_ms"`
}

// Credentials returns a usable credential pair for this workspace.
// Caller supplies the shared cookie (see SharedCookie).
func (w *Workspace) Credentials(cookie string) *Credentials {
	return &Credentials{Token: w.Token, Cookie: cookie}
}

// internalTeamPrefix parses the workspace's internal numeric id out of an
// xoxc- token: `xoxc-<INTERNAL_TEAM>-<INTERNAL_USER>-<...>-<64-hex>`.
// Used to dedupe candidate tokens before paying for an auth.test call.
var internalTeamPrefixRe = regexp.MustCompile(`^xoxc-(\d+)-`)

func internalTeamPrefix(token string) string {
	m := internalTeamPrefixRe.FindStringSubmatch(token)
	if m == nil {
		return ""
	}
	return m[1]
}

var (
	cookieOnce sync.Once
	cookieVal  string
	cookieErr  error
)

// SharedCookie returns the macOS Slack session cookie (the `xoxd-...`
// payload of the `d` cookie). Decrypted at most once per process — the
// cookie is account-level, shared across every workspace.
func SharedCookie(src *Source) (string, error) {
	cookieOnce.Do(func() {
		key, kerr := KeychainKey(src.IsAppStore)
		if kerr != nil {
			cookieErr = kerr
			return
		}
		cookieVal, cookieErr = DecryptCookie(src.CookiesDB, key)
	})
	return cookieVal, cookieErr
}

// ListWorkspaces returns every signed-in workspace.
//
// If forceRefresh is false and the cache is non-empty, the cache is
// returned without touching Slack. Trust-on-first-use: cached tokens are
// only invalidated when a subsequent API call fails with invalid_auth (the
// slack client then calls ListWorkspaces with forceRefresh=true).
//
// Refresh path:
//  1. Extract every xoxc- token candidate from LevelDB.
//  2. Group by the token's embedded internal team prefix; keep the
//     longest candidate per group.
//  3. Validate each via auth.test to learn the public team_id, team name,
//     domain, and user.
//  4. Persist workspaces/<team_id>.json per workspace.
func ListWorkspaces(src *Source, forceRefresh bool) ([]Workspace, error) {
	if !forceRefresh {
		if cached, err := loadAllWorkspaces(src); err == nil && len(cached) > 0 {
			return cached, nil
		}
	}

	cookie, err := SharedCookie(src)
	if err != nil {
		return nil, fmt.Errorf("load cookie: %w", err)
	}

	candidates, err := ExtractTokens(src.LevelDBDir)
	if err != nil {
		return nil, fmt.Errorf("extract tokens: %w", err)
	}

	longestPerTeam := map[string]string{}
	for _, t := range candidates {
		id := internalTeamPrefix(t)
		if id == "" {
			continue
		}
		if cur, ok := longestPerTeam[id]; !ok || len(t) > len(cur) {
			longestPerTeam[id] = t
		}
	}

	now := time.Now().UnixMilli()
	var found []Workspace
	for _, token := range longestPerTeam {
		ws, err := describeWorkspace(token, cookie)
		if err != nil {
			continue
		}
		ws.LastSeenMS = now
		if err := saveWorkspace(src, ws); err != nil {
			return nil, fmt.Errorf("cache workspace %s: %w", ws.TeamID, err)
		}
		found = append(found, *ws)
	}

	if len(found) == 0 {
		return nil, errors.New("no valid Slack workspace tokens found. Is Slack signed in?")
	}
	sortWorkspaces(found)
	return found, nil
}

// ResolveWorkspace looks up a workspace by exact match on team_id or
// team_domain. Tries the cache first; if no match, force-refreshes once
// in case the workspace was added after the cache was written.
func ResolveWorkspace(src *Source, identifier string) (*Workspace, error) {
	if identifier == "" {
		return nil, errors.New("empty workspace identifier")
	}
	if ws, ok := findIn(src, identifier, false); ok {
		return ws, nil
	}
	if ws, ok := findIn(src, identifier, true); ok {
		return ws, nil
	}
	return nil, fmt.Errorf("workspace %q not found. Try `spy workspaces` to list signed-in workspaces", identifier)
}

func findIn(src *Source, identifier string, forceRefresh bool) (*Workspace, bool) {
	workspaces, err := ListWorkspaces(src, forceRefresh)
	if err != nil {
		return nil, false
	}
	for i := range workspaces {
		ws := &workspaces[i]
		if ws.TeamID == identifier || ws.TeamDomain == identifier {
			return ws, true
		}
	}
	return nil, false
}

// DefaultWorkspace returns the workspace to use when no --workspace was
// specified.
//
// Priority:
//  1. Config: config.json's default_workspace.
//  2. Exactly one signed-in workspace.
//  3. Otherwise: error with the list of choices.
func DefaultWorkspace(src *Source) (*Workspace, error) {
	cfg, err := LoadConfig(src)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	if cfg.DefaultWorkspace != "" {
		return ResolveWorkspace(src, cfg.DefaultWorkspace)
	}

	workspaces, err := ListWorkspaces(src, false)
	if err != nil {
		return nil, err
	}
	if len(workspaces) == 1 {
		ws := workspaces[0]
		return &ws, nil
	}

	var lines []string
	for _, ws := range workspaces {
		lines = append(lines, fmt.Sprintf("  %s  (%s)  — %s", ws.TeamDomain, ws.TeamID, ws.TeamName))
	}
	return nil, fmt.Errorf(
		"multiple workspaces signed in; pass --workspace <domain|team_id> or set a default with `spy workspaces use <id>`:\n%s",
		strings.Join(lines, "\n"))
}

// describeWorkspace calls auth.test with a candidate token to learn the
// workspace's public identity.
func describeWorkspace(token, cookie string) (*Workspace, error) {
	res, err := Test(&Credentials{Token: token, Cookie: cookie})
	if err != nil {
		return nil, err
	}
	if !res.Ok {
		return nil, fmt.Errorf("auth.test: %s", res.Error)
	}
	return &Workspace{
		TeamID:     res.TeamID,
		TeamName:   res.Team,
		TeamDomain: extractDomain(res.URL),
		UserID:     res.UserID,
		UserName:   res.User,
		URL:        res.URL,
		Token:      token,
	}, nil
}

// extractDomain pulls "shopifyalumnigroup" out of
// "https://shopifyalumnigroup.slack.com/".
func extractDomain(url string) string {
	s := strings.TrimPrefix(url, "https://")
	s = strings.TrimPrefix(s, "http://")
	if i := strings.Index(s, "."); i > 0 {
		return s[:i]
	}
	return ""
}

func sortWorkspaces(ws []Workspace) {
	sort.Slice(ws, func(i, j int) bool { return ws[i].TeamDomain < ws[j].TeamDomain })
}

// ── disk I/O ───────────────────────────────────────────────

func loadAllWorkspaces(src *Source) ([]Workspace, error) {
	entries, err := os.ReadDir(src.WorkspacesDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	var out []Workspace
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		ws, err := loadWorkspace(src, strings.TrimSuffix(e.Name(), ".json"))
		if err != nil {
			continue
		}
		out = append(out, *ws)
	}
	sortWorkspaces(out)
	return out, nil
}

func loadWorkspace(src *Source, teamID string) (*Workspace, error) {
	data, err := os.ReadFile(src.WorkspaceCachePath(teamID))
	if err != nil {
		return nil, err
	}
	var ws Workspace
	if err := json.Unmarshal(data, &ws); err != nil {
		return nil, err
	}
	return &ws, nil
}

func saveWorkspace(src *Source, ws *Workspace) error {
	if err := os.MkdirAll(src.WorkspacesDir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(ws, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(src.WorkspaceCachePath(ws.TeamID), data, 0o600)
}
