// Package s3 is a placeholder for the S3-compatible StorageBackend (AWS/MinIO/R2/Ceph),
// which supports signed GET/PUT (§05.3). TODO: implement against an S3 SDK. Kept as a
// compiling stub so the driver seam is visible in the scaffold.
package s3

import (
	"context"
	"io"
	"time"

	"github.com/proseria-research/lineage/internal/domain"
)

type Backend struct {
	name   string
	bucket string
}

func New(name, bucket string) *Backend { return &Backend{name: name, bucket: bucket} }

var _ domain.StorageBackend = (*Backend)(nil)

func (b *Backend) Name() string { return b.name }

func (b *Backend) Capabilities() domain.StorageCapabilities {
	return domain.StorageCapabilities{Signing: true, Ranges: true, Multipart: true}
}

func (b *Backend) Stat(context.Context, string) (domain.ObjectInfo, error) {
	return domain.ObjectInfo{}, errTODO
}
func (b *Backend) SignGet(context.Context, string, time.Duration) (string, error) {
	return "", errTODO
}
func (b *Backend) SignPut(context.Context, string, time.Duration) (domain.SignedRequest, error) {
	return domain.SignedRequest{}, errTODO
}
func (b *Backend) Get(context.Context, string) (io.ReadCloser, error) { return nil, errTODO }
func (b *Backend) Delete(context.Context, string) error               { return errTODO }

var errTODO = domain.Internal("s3 backend not yet implemented")
