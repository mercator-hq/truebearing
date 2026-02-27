package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
)

// GenerateKeypair creates a new Ed25519 keypair for the named agent and writes both key
// files to <dir>/keys/. The private key is written as PKCS8 PEM to <name>.pem and the
// public key as PKIX PEM to <name>.pub.pem. Both files are created with 0600 permissions.
//
// Using PKCS8 for the private key and PKIX for the public key matches the formats expected
// by crypto/x509, the standard library's parse/marshal functions, and common tooling (openssl).
func GenerateKeypair(name, dir string) (ed25519.PublicKey, ed25519.PrivateKey, error) {
	pubKey, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generating Ed25519 keypair: %w", err)
	}

	keysDir := filepath.Join(dir, "keys")
	// 0700 so only the owner can list the keys directory itself.
	if err := os.MkdirAll(keysDir, 0700); err != nil {
		return nil, nil, fmt.Errorf("creating keys directory %s: %w", keysDir, err)
	}

	privPath := filepath.Join(keysDir, name+".pem")
	if err := writePrivateKey(privPath, privKey); err != nil {
		return nil, nil, err
	}

	pubPath := filepath.Join(keysDir, name+".pub.pem")
	if err := writePublicKey(pubPath, pubKey); err != nil {
		return nil, nil, err
	}

	return pubKey, privKey, nil
}

// LoadPrivateKey reads and parses a PKCS8 PEM-encoded Ed25519 private key from path.
func LoadPrivateKey(path string) (ed25519.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading private key from %s: %w", path, err)
	}

	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("decoding PEM from %s: no PEM block found", path)
	}

	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parsing PKCS8 private key from %s: %w", path, err)
	}

	privKey, ok := key.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("key at %s is not an Ed25519 private key", path)
	}

	return privKey, nil
}

// LoadPublicKey reads and parses a PKIX PEM-encoded Ed25519 public key from path.
func LoadPublicKey(path string) (ed25519.PublicKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading public key from %s: %w", path, err)
	}

	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("decoding PEM from %s: no PEM block found", path)
	}

	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parsing PKIX public key from %s: %w", path, err)
	}

	pubKey, ok := key.(ed25519.PublicKey)
	if !ok {
		return nil, fmt.Errorf("key at %s is not an Ed25519 public key", path)
	}

	return pubKey, nil
}

// writePrivateKey marshals privKey to PKCS8 DER, wraps it in a PEM block, and writes
// it to path with 0600 permissions.
func writePrivateKey(path string, privKey ed25519.PrivateKey) error {
	der, err := x509.MarshalPKCS8PrivateKey(privKey)
	if err != nil {
		return fmt.Errorf("marshalling private key to PKCS8: %w", err)
	}

	data := pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: der,
	})

	// 0600: owner read/write only — no group or other access. Private keys are secrets.
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("writing private key to %s: %w", path, err)
	}

	return nil
}

// writePublicKey marshals pubKey to PKIX DER, wraps it in a PEM block, and writes
// it to path with 0600 permissions.
func writePublicKey(path string, pubKey ed25519.PublicKey) error {
	der, err := x509.MarshalPKIXPublicKey(pubKey)
	if err != nil {
		return fmt.Errorf("marshalling public key to PKIX: %w", err)
	}

	data := pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: der,
	})

	// Design: public key files also use 0600 (not 0644) to prevent other local users
	// from enumerating which agents are registered on the machine. The public key is not
	// secret, but agent names in the filename are operational information.
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("writing public key to %s: %w", path, err)
	}

	return nil
}
