// Package fs is a filesystem StorageBackend (local / PVC). It cannot sign, so the API
// falls back to stream-through delivery for it (§05.3, §05.7). Good for dev/air-gapped.
package fs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/proseria-research/lineage/internal/domain"
)

type Backend struct {
	name string
	root string
}

func New(name, root string) *Backend { return &Backend{name: name, root: root} }

var _ domain.StorageBackend = (*Backend)(nil)

func (b *Backend) Name() string { return b.name }

func (b *Backend) Capabilities() domain.StorageCapabilities {
	return domain.StorageCapabilities{Signing: false, Ranges: true}
}

// path resolves a file:// uri (or bare path) to an absolute path under root.
func (b *Backend) path(uri string) string {
	p := strings.TrimPrefix(uri, "file://")
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(b.root, p)
}

func (b *Backend) Stat(_ context.Context, uri string) (domain.ObjectInfo, error) {
	f, err := os.Open(b.path(uri))
	if err != nil {
		if os.IsNotExist(err) {
			return domain.ObjectInfo{Exists: false}, nil
		}
		return domain.ObjectInfo{}, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return domain.ObjectInfo{}, err
	}
	return domain.ObjectInfo{Exists: true, SizeBytes: n, Digest: "sha256:" + hex.EncodeToString(h.Sum(nil))}, nil
}

// SignGet is unsupported for fs — callers fall back to stream-through (§05.7).
func (b *Backend) SignGet(_ context.Context, _ string, _ time.Duration) (string, error) {
	return "", domain.ErrStorageUnsupported
}

func (b *Backend) SignPut(_ context.Context, _ string, _ time.Duration) (domain.SignedRequest, error) {
	return domain.SignedRequest{}, domain.ErrStorageUnsupported
}

func (b *Backend) Get(_ context.Context, uri string) (io.ReadCloser, error) {
	return os.Open(b.path(uri))
}

func (b *Backend) Delete(_ context.Context, uri string) error {
	return os.Remove(b.path(uri))
}
