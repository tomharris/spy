package auth

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// TestResult mirrors the auth.test Slack response we care about.
type TestResult struct {
	Ok     bool   `json:"ok"`
	Error  string `json:"error,omitempty"`
	URL    string `json:"url,omitempty"`
	Team   string `json:"team,omitempty"`
	User   string `json:"user,omitempty"`
	TeamID string `json:"team_id,omitempty"`
	UserID string `json:"user_id,omitempty"`
}

// Test calls Slack's auth.test with the given credentials. Useful both as a
// validation step (token + cookie work together) and as the body of the
// `spy auth` command.
func Test(c *Credentials) (*TestResult, error) {
	req, err := http.NewRequest(http.MethodGet, "https://slack.com/api/auth.test", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Cookie", "d="+c.Cookie)

	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("auth.test request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("auth.test read: %w", err)
	}

	var out TestResult
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("auth.test decode: %w", err)
	}
	return &out, nil
}
