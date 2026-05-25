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
// store and decrypts it with the Keychain-derived key.
//
// Format: leading "v10" prefix + AES-128-CBC ciphertext, key derived via
// PBKDF2-SHA1(password="<keychain key>", salt="saltysalt", iter=1003, len=16),
// IV = 16 space bytes, PKCS7 padding.
func DecryptCookie(cookiesDB string, keychainKey []byte) (string, error) {
	tmp, err := copyToTemp(cookiesDB)
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

	if len(encrypted) < 3 || !bytes.Equal(encrypted[:3], []byte("v10")) {
		return "", errors.New("unknown cookie encryption format (expected v10 prefix)")
	}
	ct := encrypted[3:]
	if len(ct)%aes.BlockSize != 0 {
		return "", fmt.Errorf("ciphertext length %d is not a multiple of AES block size", len(ct))
	}

	aesKey := pbkdf2.Key(keychainKey, []byte("saltysalt"), 1003, 16, sha1.New)
	iv := bytes.Repeat([]byte{' '}, aes.BlockSize)

	block, err := aes.NewCipher(aesKey)
	if err != nil {
		return "", fmt.Errorf("aes cipher: %w", err)
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

	text := string(pt)
	idx := strings.Index(text, "xoxd-")
	if idx < 0 {
		return "", errors.New("no xoxd- prefix found in decrypted cookie")
	}
	return text[idx:], nil
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
