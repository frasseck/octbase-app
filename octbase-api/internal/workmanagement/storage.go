package workmanagement

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// AttachmentStorage stores uploaded attachment bytes on a local filesystem
// volume. Files are addressed by an opaque, server-generated storage key — never
// by the user-supplied filename — which neutralizes path-traversal attempts in
// filenames. Keys are sharded into two-character subdirectories to keep any one
// directory from growing unbounded.
//
// Object storage (S3/MinIO) is intentionally out of scope; see docs/operations.md.
type AttachmentStorage struct {
	root string
}

// NewAttachmentStorage returns a storage rooted at dir, creating it if needed.
func NewAttachmentStorage(dir string) (*AttachmentStorage, error) {
	if dir == "" {
		return nil, errors.New("attachment storage dir must not be empty")
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("create attachment storage dir: %w", err)
	}
	return &AttachmentStorage{root: dir}, nil
}

// NewStorageKey returns a fresh random 256-bit key, hex-encoded. The key is the
// only thing that ties a DB row to a file on disk; it is opaque and unguessable.
func NewStorageKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// pathFor resolves a storage key to its on-disk path, refusing keys that are not
// pure hex of the expected length (defense in depth against traversal even
// though keys are server-generated).
func (s *AttachmentStorage) pathFor(key string) (string, error) {
	if len(key) != 64 || !isHex(key) {
		return "", fmt.Errorf("invalid storage key")
	}
	shard := key[:2]
	p := filepath.Join(s.root, shard, key)
	// Ensure the resolved path stays within root.
	absRoot, err := filepath.Abs(s.root)
	if err != nil {
		return "", err
	}
	absP, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(absP, absRoot+string(os.PathSeparator)) {
		return "", fmt.Errorf("resolved path escapes storage root")
	}
	return p, nil
}

func isHexChar(c rune) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')
}

func isHex(s string) bool {
	for _, c := range s {
		if !isHexChar(c) {
			return false
		}
	}
	return true
}

// Write streams r into the file identified by key, returning the number of bytes
// written.
func (s *AttachmentStorage) Write(key string, r io.Reader) (int64, error) {
	p, err := s.pathFor(key)
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return 0, fmt.Errorf("create shard dir: %w", err)
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640) // #nosec G302 G304 -- group-readable is deliberate (container runs UID 10001 with group 0); p is built from server-generated UUIDs under the attachments root
	if err != nil {
		return 0, fmt.Errorf("open attachment file: %w", err)
	}
	n, copyErr := io.Copy(f, r)
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(p)
		return 0, copyErr
	}
	if closeErr != nil {
		_ = os.Remove(p)
		return 0, closeErr
	}
	return n, nil
}

// Open returns a readable handle to the file identified by key.
func (s *AttachmentStorage) Open(key string) (*os.File, error) {
	p, err := s.pathFor(key)
	if err != nil {
		return nil, err
	}
	return os.Open(p) // #nosec G304 -- p is built from server-generated UUIDs under the attachments root
}

// Remove deletes the file identified by key, tolerating an already-missing file.
func (s *AttachmentStorage) Remove(key string) error {
	if key == "" {
		return nil
	}
	p, err := s.pathFor(key)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Copy duplicates the bytes at srcKey into a new storage key and returns it. Used
// by CopyTask so a copied task's files have an independent lifecycle (deleting
// either task never orphans or prematurely removes the other's bytes).
func (s *AttachmentStorage) Copy(srcKey string) (string, error) {
	src, err := s.Open(srcKey)
	if err != nil {
		return "", err
	}
	defer func() { _ = src.Close() }()
	dstKey, err := NewStorageKey()
	if err != nil {
		return "", err
	}
	if _, err := s.Write(dstKey, src); err != nil {
		return "", err
	}
	return dstKey, nil
}
