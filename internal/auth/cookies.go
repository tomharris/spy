package auth

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha1"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/crypto/pbkdf2"
	_ "modernc.org/sqlite"
)

// DecryptCookie reads the encrypted `d` cookie from Slack's SQLite cookie
// store and decrypts it.
//
// The encrypted value is a 3-byte version prefix ("v10"/"v11") + AES-128-CBC
// ciphertext. The prefix selects the key source and iteration count, which
// differ per platform: cookieKey (implemented in keychain_darwin.go /
// keyring_linux.go) resolves the prefix to a (password, iterations) pair. The
// AES-128-CBC pipeline itself — PBKDF2-SHA1(salt="saltysalt", len=16), a
// 16-space IV, and PKCS7 padding — is identical everywhere.
func DecryptCookie(src *Source) (string, error) {
	tmp, err := copyToTemp(src.CookiesDB)
	if err != nil {
		return "", fmt.Errorf("copy cookies db: %w", err)
	}
	defer os.Remove(tmp)

	db, err := sql.Open("sqlite", tmp)
	if err != nil {
		return "", fmt.Errorf("open cookies db: %w", err)
	}
	defer db.Close()

	var encrypted []byte
	row := db.QueryRow(`SELECT encrypted_value FROM cookies WHERE name='d' AND host_key='.slack.com' LIMIT 1`)
	if err := row.Scan(&encrypted); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", errors.New("no 'd' cookie found in Slack cookie store")
		}
		return "", fmt.Errorf("read cookie row: %w", err)
	}

	if len(encrypted) < 3 {
		return "", errors.New("cookie value too short to contain a version prefix")
	}
	prefix := string(encrypted[:3])
	if prefix != "v10" && prefix != "v11" {
		return "", fmt.Errorf("unknown cookie encryption format (expected v10/v11 prefix, got %q)", prefix)
	}

	password, iterations, err := cookieKey(src, prefix)
	if err != nil {
		return "", err
	}

	pt, err := aesDecrypt(encrypted[3:], password, iterations)
	if err != nil {
		return "", err
	}

	text := string(pt)
	idx := strings.Index(text, "xoxd-")
	if idx < 0 {
		return "", errors.New("no xoxd- prefix found in decrypted cookie")
	}
	return text[idx:], nil
}

// aesDecrypt runs Chromium's cookie/credential CBC scheme: derive a 16-byte
// AES-128 key via PBKDF2-SHA1(password, salt="saltysalt", iterations), decrypt
// with a 16-space IV, and strip PKCS7 padding. The iteration count is the only
// parameter that varies by platform (1003 on macOS, 1 on Linux).
func aesDecrypt(ct, password []byte, iterations int) ([]byte, error) {
	if len(ct)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("ciphertext length %d is not a multiple of AES block size", len(ct))
	}

	aesKey := pbkdf2.Key(password, []byte("saltysalt"), iterations, 16, sha1.New)
	iv := bytes.Repeat([]byte{' '}, aes.BlockSize)

	block, err := aes.NewCipher(aesKey)
	if err != nil {
		return nil, fmt.Errorf("aes cipher: %w", err)
	}
	mode := cipher.NewCBCDecrypter(block, iv)

	pt := make([]byte, len(ct))
	mode.CryptBlocks(pt, ct)

	// PKCS7 unpad (defensive: only if last byte is a plausible pad length).
	if n := len(pt); n > 0 {
		padLen := int(pt[n-1])
		if padLen > 0 && padLen <= aes.BlockSize {
			pt = pt[:n-padLen]
		}
	}
	return pt, nil
}

// copyToTemp copies a file to a tempfile so we can read SQLite databases
// without contending with a running Slack process for the same file.
func copyToTemp(src string) (string, error) {
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()

	out, err := os.CreateTemp("", "spy_cookies_*.db")
	if err != nil {
		return "", err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		os.Remove(out.Name())
		return "", err
	}
	return out.Name(), nil
}
