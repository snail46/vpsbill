package backup

import (
	"bytes"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func testEncryption(t *testing.T) Encryption {
	t.Helper()
	e, err := newEncryption()
	if err != nil {
		t.Fatal(err)
	}
	e.N = 1 << 14 // the smallest accepted, to keep the test quick
	e.ChunkSize = 1024
	return e
}

func TestEncryptRoundTrip(t *testing.T) {
	e := testEncryption(t)
	for _, size := range []int{0, 1, 1024, 3000, 4096} {
		dir := t.TempDir()
		plain := make([]byte, size)
		_, _ = rand.Read(plain)
		source, sealed, opened := filepath.Join(dir, "a"), filepath.Join(dir, "b"), filepath.Join(dir, "c")
		if err := os.WriteFile(source, plain, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := encryptFile(source, sealed, "correct horse battery", e); err != nil {
			t.Fatal(err)
		}
		if body, _ := os.ReadFile(sealed); size > 16 && bytes.Contains(body, plain[:16]) {
			t.Fatalf("size %d: plaintext visible", size)
		}
		if err := decryptFile(sealed, opened, "correct horse battery", e); err != nil {
			t.Fatalf("size %d: %v", size, err)
		}
		if got, _ := os.ReadFile(opened); !bytes.Equal(got, plain) {
			t.Fatalf("size %d: round trip changed the data", size)
		}
		if err := decryptFile(sealed, "", "correct horse battery", e); err != nil {
			t.Fatalf("size %d: first-chunk check: %v", size, err)
		}
		if err := decryptFile(sealed, "", "wrong passphrase!!", e); !errors.Is(err, ErrWrongPassphrase) {
			t.Fatalf("size %d: wrong passphrase gave %v", size, err)
		}
	}
}

func TestEncryptedBackupDetectsTampering(t *testing.T) {
	e := testEncryption(t)
	dir := t.TempDir()
	plain := make([]byte, 5000)
	_, _ = rand.Read(plain)
	source, sealed := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	if err := os.WriteFile(source, plain, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := encryptFile(source, sealed, "correct horse battery", e); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(sealed)
	chunk := 4 + 1024 + 16

	// Dropping the last chunk must not pass as a shorter backup.
	cut := filepath.Join(dir, "cut")
	if err := os.WriteFile(cut, body[:4*chunk], 0o600); err != nil {
		t.Fatal(err)
	}
	if err := decryptFile(cut, filepath.Join(dir, "out1"), "correct horse battery", e); err == nil {
		t.Fatal("truncated backup decrypted")
	}
	// Nor may a flipped byte in a later chunk.
	flipped := append([]byte(nil), body...)
	flipped[2*chunk+10] ^= 1
	bad := filepath.Join(dir, "bad")
	if err := os.WriteFile(bad, flipped, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := decryptFile(bad, filepath.Join(dir, "out2"), "correct horse battery", e); err == nil {
		t.Fatal("tampered backup decrypted")
	}
}
