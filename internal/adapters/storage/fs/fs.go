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

// Put streams bytes to <root>/<path> (creating parent dirs) and returns a file:// uri.
// size/contentType are ignored — fs writes whatever it is handed (§05.6).
func (b *Backend) Put(_ context.Context, path string, r io.Reader, _ int64, _ string) (string, error) {
	dst := b.path(path)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}
	f, err := os.Create(dst)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return "file://" + path, nil
}

// URIFor returns the file:// uri for a stored path (§05.5).
func (b *Backend) URIFor(path string) string {
	return "file://" + strings.TrimPrefix(path, "/")
}

func (b *Backend) Delete(_ context.Context, uri string) error {
	return os.Remove(b.path(uri))
}

// fs cannot presign, so it cannot offer client-driven multipart uploads; large files use
// the stream-through path instead (§05.6). These satisfy the port with ErrStorageUnsupported.
func (b *Backend) InitiateMultipart(context.Context, string, int, int64, time.Duration) (domain.MultipartPlan, error) {
	return domain.MultipartPlan{}, domain.ErrStorageUnsupported
}
func (b *Backend) CompleteMultipart(context.Context, string, string, []domain.MultipartPart) (string, error) {
	return "", domain.ErrStorageUnsupported
}
func (b *Backend) AbortMultipart(context.Context, string, string) error {
	return domain.ErrStorageUnsupported
}

// ListObjects walks root and returns every file as a file:// uri (§05.8, GC).
func (b *Backend) ListObjects(_ context.Context, prefix string) ([]domain.ObjectRef, error) {
	base := b.path(strings.TrimPrefix(prefix, "file://"))
	var out []domain.ObjectRef
	err := filepath.WalkDir(base, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) { // nothing stored yet
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(b.root, p)
		if err != nil {
			return err
		}
		out = append(out, domain.ObjectRef{
			URI: "file://" + filepath.ToSlash(rel), SizeBytes: info.Size(),
			ModifiedAt: info.ModTime().UnixMilli(),
		})
		return nil
	})
	return out, err
}
