package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"
)

type tokenCacheFile struct {
	Token string `json:"token"`
	TS    int64  `json:"ts"`
}

// GetCredentials returns a working (token, cookie) pair, doing the minimum
// work needed to get there.
//
// Resolution order:
//  1. Disk cache (~/.local/spy/token-cache.json) → validate via auth.test
//  2. Fresh extraction from Slack LevelDB → validate each candidate
//
// If forceRefresh is true, the disk cache is bypassed.
func GetCredentials(src *Source, forceRefresh bool) (*Credentials, error) {
	cookie, err := getCookie(src)
	if err != nil {
		return nil, fmt.Errorf("decrypt cookie: %w", err)
	}

	if !forceRefresh {
		if token, ok := loadCache(src.CachePath); ok {
			c := &Credentials{Token: token, Cookie: cookie}
			if isValid(c) {
				return c, nil
			}
		}
	}

	candidates, err := ExtractTokens(src.LevelDBDir)
	if err != nil {
		return nil, fmt.Errorf("extract tokens: %w", err)
	}

	for _, t := range candidates {
		c := &Credentials{Token: t, Cookie: cookie}
		if isValid(c) {
			saveCache(src, t)
			return c, nil
		}
	}

	return nil, errors.New("none of the extracted xoxc- tokens passed auth.test")
}

func getCookie(src *Source) (string, error) {
	key, err := KeychainKey(src.IsAppStore)
	if err != nil {
		return "", err
	}
	return DecryptCookie(src.CookiesDB, key)
}

func isValid(c *Credentials) bool {
	res, err := Test(c)
	return err == nil && res.Ok
}

func loadCache(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	var c tokenCacheFile
	if err := json.Unmarshal(data, &c); err != nil {
		return "", false
	}
	return c.Token, c.Token != ""
}

func saveCache(src *Source, token string) {
	if err := os.MkdirAll(src.CacheDir, 0o700); err != nil {
		return
	}
	data, err := json.Marshal(tokenCacheFile{Token: token, TS: time.Now().UnixMilli()})
	if err != nil {
		return
	}
	_ = os.WriteFile(src.CachePath, data, 0o600)
}
