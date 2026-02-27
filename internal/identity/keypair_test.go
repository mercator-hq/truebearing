package identity_test

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"testing"

	"github.com/mercator-hq/truebearing/internal/identity"
)

// TestGenerateKeypair_RoundTrip verifies that keys written to disk by GenerateKeypair
// can be loaded back and are cryptographically equivalent to the originals.
func TestGenerateKeypair_RoundTrip(t *testing.T) {
	dir := t.TempDir()

	pubKey, privKey, err := identity.GenerateKeypair("test-agent", dir)
	if err != nil {
		t.Fatalf("GenerateKeypair: %v", err)
	}

	loadedPriv, err := identity.LoadPrivateKey(filepath.Join(dir, "keys", "test-agent.pem"))
	if err != nil {
		t.Fatalf("LoadPrivateKey: %v", err)
	}

	loadedPub, err := identity.LoadPublicKey(filepath.Join(dir, "keys", "test-agent.pub.pem"))
	if err != nil {
		t.Fatalf("LoadPublicKey: %v", err)
	}

	if !privKey.Equal(loadedPriv) {
		t.Error("loaded private key does not match generated private key")
	}

	if !pubKey.Equal(loadedPub) {
		t.Error("loaded public key does not match generated public key")
	}

	// Verify the public key can be derived from the loaded private key — confirms the
	// private key file encodes the full key material, not just the seed.
	derived, ok := loadedPriv.Public().(ed25519.PublicKey)
	if !ok {
		t.Fatal("could not cast derived public key to ed25519.PublicKey")
	}
	if !pubKey.Equal(derived) {
		t.Error("public key derived from loaded private key does not match generated public key")
	}
}

// TestGenerateKeypair_SignVerify confirms that the loaded key pair can sign and verify,
// not just that the bytes are equal. This catches encoding bugs that preserve bytes but
// produce keys that fail at use time.
func TestGenerateKeypair_SignVerify(t *testing.T) {
	dir := t.TempDir()

	_, privKey, err := identity.GenerateKeypair("sign-test", dir)
	if err != nil {
		t.Fatalf("GenerateKeypair: %v", err)
	}

	loadedPub, err := identity.LoadPublicKey(filepath.Join(dir, "keys", "sign-test.pub.pem"))
	if err != nil {
		t.Fatalf("LoadPublicKey: %v", err)
	}

	msg := []byte("truebearing test payload")
	sig := ed25519.Sign(privKey, msg)

	if !ed25519.Verify(loadedPub, msg, sig) {
		t.Error("signature verification failed: loaded public key does not verify signature from generated private key")
	}
}

// TestGenerateKeypair_FilePermissions asserts that both key files are written with 0600
// permissions. This is a security invariant — CLAUDE.md §8 rule 3.
func TestGenerateKeypair_FilePermissions(t *testing.T) {
	dir := t.TempDir()

	if _, _, err := identity.GenerateKeypair("perm-test", dir); err != nil {
		t.Fatalf("GenerateKeypair: %v", err)
	}

	cases := []string{"perm-test.pem", "perm-test.pub.pem"}
	for _, name := range cases {
		path := filepath.Join(dir, "keys", name)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("Stat(%s): %v", path, err)
		}

		if got := info.Mode().Perm(); got != 0600 {
			t.Errorf("file %s has permissions %04o, want 0600", name, got)
		}
	}
}

// TestLoadPrivateKey_NotExist verifies that loading from a missing path returns a
// descriptive error rather than panicking or returning nil.
func TestLoadPrivateKey_NotExist(t *testing.T) {
	_, err := identity.LoadPrivateKey("/nonexistent/path/key.pem")
	if err == nil {
		t.Error("expected error loading non-existent private key, got nil")
	}
}

// TestLoadPublicKey_NotExist mirrors the private key test for the public key loader.
func TestLoadPublicKey_NotExist(t *testing.T) {
	_, err := identity.LoadPublicKey("/nonexistent/path/key.pub.pem")
	if err == nil {
		t.Error("expected error loading non-existent public key, got nil")
	}
}

// TestGenerateKeypair_KeysDirectory verifies that GenerateKeypair creates the keys/
// subdirectory inside dir if it does not yet exist, and writes files at the correct paths.
func TestGenerateKeypair_KeysDirectory(t *testing.T) {
	dir := t.TempDir()

	if _, _, err := identity.GenerateKeypair("dir-test", dir); err != nil {
		t.Fatalf("GenerateKeypair: %v", err)
	}

	for _, name := range []string{"dir-test.pem", "dir-test.pub.pem"} {
		path := filepath.Join(dir, "keys", name)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected key file at %s, got error: %v", path, err)
		}
	}
}
