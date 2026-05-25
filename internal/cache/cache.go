// Package cache is a per-path, TTL'd JSON cache. Used to memoize per-workspace
// users and channels listings so an agent firing many spy commands in
// sequence doesn't re-paginate every call.
package cache

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// envelope wraps cached data with the timestamp we need to honor TTL.
type envelope[T any] struct {
	CachedAtMS int64 `json:"cached_at_ms"`
	Data       T     `json:"data"`
}

// Load reads path; returns (data, true) if the cache exists and is fresher
// than ttl. Any error (missing file, malformed JSON, expired entry) returns
// the zero value and false — callers should re-fetch.
func Load[T any](path string, ttl time.Duration) (T, bool) {
	var zero T
	data, err := os.ReadFile(path)
	if err != nil {
		return zero, false
	}
	var e envelope[T]
	if err := json.Unmarshal(data, &e); err != nil {
		return zero, false
	}
	age := time.Since(time.UnixMilli(e.CachedAtMS))
	if age > ttl {
		return zero, false
	}
	return e.Data, true
}

// Save writes data to path with the current timestamp. Creates parent
// directories as needed (mode 0700).
func Save[T any](path string, data T) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(envelope[T]{
		CachedAtMS: time.Now().UnixMilli(),
		Data:       data,
	})
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

// Invalidate removes a cache file. Returns nil if the file didn't exist.
func Invalidate(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
