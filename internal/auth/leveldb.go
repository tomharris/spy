package auth

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/opt"
)

var xoxcTokenRe = regexp.MustCompile(`xoxc-\d+-\d+-\d+-[a-f0-9]{64}`)

// ExtractTokens copies Slack's LevelDB store to a tempdir, opens it via
// goleveldb (which transparently decompresses Snappy blocks), and scans
// every value for xoxc- tokens.
//
// The copy is required because the live Slack process holds an exclusive
// lock on the LevelDB while running. The previous raw-file regex approach
// could not see tokens stored inside compressed blocks — which, after
// compaction, is where they normally live.
func ExtractTokens(leveldbDir string) ([]string, error) {
	tmp, err := copyLevelDB(leveldbDir)
	if err != nil {
		return nil, fmt.Errorf("copy leveldb: %w", err)
	}
	defer os.RemoveAll(tmp)

	db, err := leveldb.OpenFile(tmp, &opt.Options{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("open leveldb: %w", err)
	}
	defer db.Close()

	tokens := make(map[string]struct{})
	iter := db.NewIterator(nil, nil)
	for iter.Next() {
		for _, m := range xoxcTokenRe.FindAll(iter.Value(), -1) {
			tokens[string(m)] = struct{}{}
		}
	}
	iter.Release()
	if err := iter.Error(); err != nil {
		return nil, fmt.Errorf("iterate leveldb: %w", err)
	}

	if len(tokens) == 0 {
		return nil, errors.New("no xoxc- token found in LevelDB. Is Slack signed in?")
	}

	out := make([]string, 0, len(tokens))
	for t := range tokens {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return len(out[i]) > len(out[j]) })
	return out, nil
}

func copyLevelDB(srcDir string) (string, error) {
	dst, err := os.MkdirTemp("", "spy_leveldb_*")
	if err != nil {
		return "", err
	}

	entries, err := os.ReadDir(srcDir)
	if err != nil {
		os.RemoveAll(dst)
		return "", err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		// Skip the LOCK file — goleveldb will create its own.
		if e.Name() == "LOCK" {
			continue
		}
		if err := copyFile(filepath.Join(srcDir, e.Name()), filepath.Join(dst, e.Name())); err != nil {
			os.RemoveAll(dst)
			return "", fmt.Errorf("copy %s: %w", e.Name(), err)
		}
	}
	return dst, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}
