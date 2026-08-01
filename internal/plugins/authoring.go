package plugins

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

const maxPublisherPrivateKeyBytes = 4 * 1024

type GeneratedPublisherKey struct {
	KeyID      string
	PublicKey  ed25519.PublicKey
	PrivateKey ed25519.PrivateKey
}

func GeneratePublisherKey() (GeneratedPublisherKey, error) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return GeneratedPublisherKey{}, wrapPluginError(ErrorInternal, "generate plugin publisher key", err)
	}
	return GeneratedPublisherKey{
		KeyID:      PublisherKeyID(publicKey),
		PublicKey:  append(ed25519.PublicKey(nil), publicKey...),
		PrivateKey: append(ed25519.PrivateKey(nil), privateKey...),
	}, nil
}

func EncodePublisherPublicKey(publicKey ed25519.PublicKey) (string, error) {
	if len(publicKey) != ed25519.PublicKeySize {
		return "", pluginError(ErrorInvalid, "encode plugin publisher key", "invalid Ed25519 public key length")
	}
	return base64.StdEncoding.EncodeToString(publicKey), nil
}

func EncodePublisherPrivateKey(privateKey ed25519.PrivateKey) (string, error) {
	if err := validatePublisherPrivateKey(privateKey); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(privateKey.Seed()), nil
}

func ParsePublisherPrivateKey(data []byte) (ed25519.PrivateKey, error) {
	encoded := strings.TrimSpace(string(data))
	encoded = strings.TrimSpace(strings.TrimPrefix(encoded, PublisherAlgorithm+":"))
	if encoded == "" || strings.ContainsAny(encoded, " \t\r\n") {
		return nil, pluginError(ErrorInvalid, "parse plugin publisher private key", "expected one base64-encoded Ed25519 seed or private key")
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, wrapPluginError(ErrorInvalid, "parse plugin publisher private key", err)
	}
	switch len(decoded) {
	case ed25519.SeedSize:
		return ed25519.NewKeyFromSeed(decoded), nil
	case ed25519.PrivateKeySize:
		privateKey := ed25519.PrivateKey(append([]byte(nil), decoded...))
		if err := validatePublisherPrivateKey(privateKey); err != nil {
			return nil, err
		}
		return privateKey, nil
	default:
		return nil, pluginError(
			ErrorInvalid,
			"parse plugin publisher private key",
			"Ed25519 private key must encode a %d-byte seed or %d-byte private key",
			ed25519.SeedSize,
			ed25519.PrivateKeySize,
		)
	}
}

func ReadPublisherPrivateKeyFile(path string) (ed25519.PrivateKey, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, pluginError(ErrorMissing, "read plugin publisher private key", "private key does not exist")
	}
	if err != nil {
		return nil, wrapPluginError(ErrorInternal, "read plugin publisher private key", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, pluginError(ErrorBlocked, "read plugin publisher private key", "private key must be a regular non-symlink file")
	}
	if info.Size() <= 0 || info.Size() > maxPublisherPrivateKeyBytes {
		return nil, pluginError(ErrorBlocked, "read plugin publisher private key", "private key size is outside the supported range")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return nil, pluginError(ErrorBlocked, "read plugin publisher private key", "private key permissions must not grant group or world access")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, wrapPluginError(ErrorInternal, "read plugin publisher private key", err)
	}
	return ParsePublisherPrivateKey(data)
}

func ReadPublisherPublicKeyFile(path string) (ed25519.PublicKey, error) {
	data, err := readBoundedRegularFile(path, maxPublisherKeyBytes)
	if err != nil {
		return nil, err
	}
	return ParsePublisherPublicKey(data)
}

func ReadIndexDraftFile(path string) (IndexDocument, error) {
	data, err := readBoundedRegularFile(path, maxIndexBytes)
	if err != nil {
		return IndexDocument{}, err
	}
	return DecodeIndexDraft(data)
}

func PublisherKeyID(publicKey ed25519.PublicKey) string {
	return publisherKeyID(publicKey)
}

func SignIndexDraft(document IndexDocument, privateKey ed25519.PrivateKey) (IndexDocument, error) {
	if err := ValidateIndexDraft(document); err != nil {
		return IndexDocument{}, err
	}
	if err := validatePublisherPrivateKey(privateKey); err != nil {
		return IndexDocument{}, err
	}
	signingBytes, err := IndexSigningBytes(document.Signed)
	if err != nil {
		return IndexDocument{}, err
	}
	publicKey := privateKey.Public().(ed25519.PublicKey)
	keyID := PublisherKeyID(publicKey)
	for _, signature := range document.Signatures {
		if signature.KeyID != keyID {
			continue
		}
		decoded, _ := base64.StdEncoding.DecodeString(signature.Signature)
		if ed25519.Verify(publicKey, signingBytes, decoded) {
			return document, nil
		}
		return IndexDocument{}, pluginError(
			ErrorBlocked,
			"sign plugin index",
			"index already contains a different signature for key %s",
			keyID,
		)
	}
	signature := ed25519.Sign(privateKey, signingBytes)
	document.Signatures = append(document.Signatures, IndexSignature{
		KeyID:     keyID,
		Algorithm: PublisherAlgorithm,
		Signature: base64.StdEncoding.EncodeToString(signature),
	})
	sort.Slice(document.Signatures, func(i, j int) bool {
		return document.Signatures[i].KeyID < document.Signatures[j].KeyID
	})
	if err := ValidateIndexDraft(document); err != nil {
		return IndexDocument{}, err
	}
	return document, nil
}

func EncodeIndexDraft(document IndexDocument) ([]byte, error) {
	if err := ValidateIndexDraft(document); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, wrapPluginError(ErrorInternal, "encode plugin index", err)
	}
	return append(data, '\n'), nil
}

func validatePublisherPrivateKey(privateKey ed25519.PrivateKey) error {
	if len(privateKey) != ed25519.PrivateKeySize {
		return pluginError(ErrorInvalid, "validate plugin publisher private key", "invalid Ed25519 private key length")
	}
	expected := ed25519.NewKeyFromSeed(privateKey.Seed())
	if !bytes.Equal(privateKey, expected) {
		return pluginError(ErrorInvalid, "validate plugin publisher private key", "private key public component does not match its seed")
	}
	return nil
}

func WriteExclusiveFile(path string, data []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if errors.Is(err, os.ErrExist) {
		return pluginError(ErrorBlocked, "write publisher file", "refusing to replace existing file %s", path)
	}
	if err != nil {
		return wrapPluginError(ErrorInternal, "write publisher file", err)
	}
	complete := false
	defer func() {
		_ = file.Close()
		if !complete {
			_ = os.Remove(path)
		}
	}()
	if err := file.Chmod(mode.Perm()); err != nil {
		return wrapPluginError(ErrorInternal, "set publisher file permissions", err)
	}
	if _, err := file.Write(data); err != nil {
		return wrapPluginError(ErrorInternal, "write publisher file", err)
	}
	if err := file.Sync(); err != nil {
		return wrapPluginError(ErrorInternal, "sync publisher file", err)
	}
	if err := file.Close(); err != nil {
		return wrapPluginError(ErrorInternal, "close publisher file", err)
	}
	complete = true
	if directory, err := os.Open(filepath.Dir(path)); err == nil {
		_ = directory.Sync()
		_ = directory.Close()
	}
	return nil
}

func PublisherKeySummary(privateKey ed25519.PrivateKey) (string, string, error) {
	if err := validatePublisherPrivateKey(privateKey); err != nil {
		return "", "", err
	}
	publicKey, ok := privateKey.Public().(ed25519.PublicKey)
	if !ok {
		return "", "", fmt.Errorf("derive Ed25519 public key")
	}
	encoded, err := EncodePublisherPublicKey(publicKey)
	if err != nil {
		return "", "", err
	}
	return PublisherKeyID(publicKey), encoded, nil
}
