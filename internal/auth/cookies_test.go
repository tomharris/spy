package auth

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha1"
	"testing"

	"golang.org/x/crypto/pbkdf2"
)

// encrypt mirrors Chromium's cookie scheme (the inverse of aesDecrypt) so the
// test can produce a known ciphertext: PBKDF2-SHA1 key derivation, AES-128-CBC
// with a 16-space IV, PKCS7 padding. No "vXX" prefix is added — aesDecrypt
// operates on the post-prefix ciphertext.
func encrypt(t *testing.T, plaintext, password []byte, iterations int) []byte {
	t.Helper()
	key := pbkdf2.Key(password, []byte("saltysalt"), iterations, 16, sha1.New)
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("aes cipher: %v", err)
	}

	pad := aes.BlockSize - len(plaintext)%aes.BlockSize
	padded := append(append([]byte{}, plaintext...), bytes.Repeat([]byte{byte(pad)}, pad)...)

	iv := bytes.Repeat([]byte{' '}, aes.BlockSize)
	ct := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ct, padded)
	return ct
}

func TestAESDecryptRoundTrip(t *testing.T) {
	cases := []struct {
		name       string
		password   string
		iterations int
	}{
		{"linux peanuts (1 iter)", "peanuts", 1},
		{"macOS keychain key (1003 iter)", "s3cret-keychain-key", 1003},
	}

	want := []byte("xoxd-some-fake-session-cookie-value-1234567890")
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ct := encrypt(t, want, []byte(tc.password), tc.iterations)
			got, err := aesDecrypt(ct, []byte(tc.password), tc.iterations)
			if err != nil {
				t.Fatalf("aesDecrypt: %v", err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("round trip mismatch:\n got  %q\n want %q", got, want)
			}
		})
	}
}

func TestAESDecryptWrongIterationCountFails(t *testing.T) {
	// A ciphertext made with 1 iteration must NOT decrypt cleanly under 1003 —
	// this is the exact macOS/Linux mismatch the platform split prevents.
	pw := []byte("peanuts")
	want := []byte("xoxd-token")
	ct := encrypt(t, want, pw, 1)

	got, err := aesDecrypt(ct, pw, 1003)
	if err == nil && bytes.Equal(got, want) {
		t.Fatal("expected wrong iteration count to fail, but it round-tripped")
	}
}

func TestAESDecryptRejectsMisalignedCiphertext(t *testing.T) {
	if _, err := aesDecrypt([]byte("not-a-block-multiple"), []byte("peanuts"), 1); err == nil {
		t.Fatal("expected error for non-block-aligned ciphertext")
	}
}
