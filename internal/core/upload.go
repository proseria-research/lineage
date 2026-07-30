package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"time"

	"github.com/proseria-research/lineage/internal/domain"
)

// This file implements the signed upload flow (§05.6, §03.6): initiate → PUT bytes →
// finalize. Two modes, chosen by the backend's Capabilities():
//
//   - signing backend (s3): initiate returns a presigned direct-PUT URL; the client PUTs
//     bytes straight to object storage; finalize Stats/verifies and records the artifact.
//   - non-signing backend (fs): initiate returns a stream-through contentUrl on the Model
//     API; the client PUTs bytes to us, we hash+write them, finalize records the artifact.
//
// Finalize verifies the client-declared digest and size before creating the (immutable)
// artifact row; the UNIQUE(version_id, name) constraint enforces write-once (§05.5).

// pendingUpload is server-side state bound to an uploadId between initiate and finalize.
type pendingUpload struct {
	id             string
	actor          string
	model, version string
	versionID      string
	backend        string
	path           string // storage path (<model>/<version>/<name>)
	uri            string // canonical native uri for path
	in             InitiateUploadInput
	streamThrough  bool
	expiresAt      int64

	// multipart (large files on a signing backend, §05.6):
	multipart       bool
	backendUploadID string

	// set by the stream-through content handler once bytes land:
	uploaded  bool
	gotDigest string
	gotSize   int64
}

// Multipart sizing (§05.6): objects at/above the threshold on a multipart-capable backend
// are split into parts of at least the S3 minimum, capped at the S3 maximum part count.
const (
	multipartThreshold = int64(64) << 20  // 64 MiB
	minPartSize        = int64(5) << 20   // 5 MiB (S3 minimum, except the last part)
	maxParts           = 10000            // S3 maximum
	streamVerifyCap    = int64(256) << 20 // above this, trust the declared digest + size, don't re-download
)

// planParts chooses a part count and size for a size-byte object.
func planParts(size int64) (parts int, partSize int64) {
	partSize = minPartSize
	if size/partSize+1 > maxParts {
		// Grow the part size (rounded up to a MiB) so we stay within maxParts.
		partSize = ((size/maxParts)/(1<<20) + 1) << 20
	}
	parts = max(int((size+partSize-1)/partSize), 1)
	return parts, partSize
}

// InitiateUploadInput describes the artifact whose bytes are about to be uploaded (§05.6).
type InitiateUploadInput struct {
	Name           string              `json:"name"`
	Kind           domain.ArtifactKind `json:"kind"`
	SizeBytes      int64               `json:"sizeBytes"`
	MediaType      string              `json:"mediaType"`
	ModelFormat    *domain.ModelFormat `json:"modelFormat"`
	ServiceAccount string              `json:"serviceAccount"`
	StorageBackend string              `json:"storageBackend"`
}

// UploadTicket is the initiate response. Exactly one of the two upload modes is populated.
type UploadTicket struct {
	UploadID string `json:"uploadId"`
	// Direct signed PUT (signing backends):
	URL     string            `json:"url,omitempty"`
	Method  string            `json:"method,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	// Stream-through (non-signing backends): PUT the bytes to ContentURL on the Model API.
	StreamThrough bool   `json:"streamThrough,omitempty"`
	ContentURL    string `json:"contentUrl,omitempty"`
	// Multipart (large files on a signing backend): PUT each part to its presigned URL, then
	// finalize with the observed per-part ETags.
	Multipart bool                   `json:"multipart,omitempty"`
	PartSize  int64                  `json:"partSize,omitempty"`
	Parts     []domain.MultipartPart `json:"parts,omitempty"`
	ExpiresAt int64                  `json:"expiresAt"`
}

// InitiateUpload validates the target, reserves an uploadId, and returns an upload ticket.
func (s *Service) InitiateUpload(ctx context.Context, actor, model, version string, in InitiateUploadInput) (*UploadTicket, error) {
	if in.Kind == "" {
		in.Kind = domain.KindModel
	}
	if !domain.ValidName(in.Name) {
		return nil, domain.Invalid("invalid artifact name '" + in.Name + "'")
	}
	v, err := s.store.GetVersion(ctx, model, version)
	if err != nil {
		return nil, err
	}
	// Fail fast on an existing name so the client doesn't upload bytes it can't finalize (§05.5).
	if arts, err := s.store.ListArtifacts(ctx, v.ID); err == nil {
		for _, a := range arts {
			if a.Name == in.Name {
				return nil, domain.Exists("artifact '" + in.Name + "' already exists on " + model + "@" + version)
			}
		}
	}
	b := s.backend(in.StorageBackend)
	if b == nil {
		return nil, domain.Invalid("unknown storage backend '" + in.StorageBackend + "'")
	}

	path := model + "/" + version + "/" + in.Name
	pu := &pendingUpload{
		id: domain.NewID(), actor: actor, model: model, version: version, versionID: v.ID,
		backend: b.Name(), path: path, uri: b.URIFor(path), in: in,
		expiresAt: domain.NowMillis() + s.uploadTTL.Milliseconds(),
	}
	ticket := &UploadTicket{UploadID: pu.id, ExpiresAt: pu.expiresAt}

	caps := b.Capabilities()
	switch {
	case caps.Signing && caps.Multipart && in.SizeBytes >= multipartThreshold:
		// Large file: hand back a presigned PUT per part (§05.6).
		parts, partSize := planParts(in.SizeBytes)
		plan, err := b.InitiateMultipart(ctx, path, parts, partSize, s.uploadTTL)
		if err != nil {
			return nil, err
		}
		pu.multipart, pu.backendUploadID = true, plan.UploadID
		ticket.Multipart, ticket.PartSize, ticket.Parts = true, plan.PartSize, plan.Parts
	case caps.Signing:
		sr, err := b.SignPut(ctx, path, s.uploadTTL)
		if err != nil {
			return nil, err
		}
		ticket.URL, ticket.Method, ticket.Headers = sr.URL, sr.Method, sr.Headers
	default:
		pu.streamThrough = true
		ticket.StreamThrough = true
		ticket.ContentURL = "/v1/models/" + model + "/versions/" + version + "/artifacts:uploadContent?uploadId=" + pu.id
	}

	s.mu.Lock()
	s.pending[pu.id] = pu
	s.mu.Unlock()
	return ticket, nil
}

// UploadContent is the stream-through sink: it hashes and writes bytes to the backend for
// non-signing drivers, recording the computed digest/size for finalize to verify (§05.6).
func (s *Service) UploadContent(ctx context.Context, uploadID string, r io.Reader, size int64) error {
	pu, err := s.takePending(uploadID, false)
	if err != nil {
		return err
	}
	if !pu.streamThrough {
		return domain.Invalid("upload '" + uploadID + "' expects a direct PUT, not stream-through")
	}
	b := s.backend(pu.backend)
	if b == nil {
		return domain.Internal("upload backend '" + pu.backend + "' unavailable")
	}
	h := sha256.New()
	uri, err := b.Put(ctx, pu.path, io.TeeReader(r, h), size, pu.in.MediaType)
	if err != nil {
		return err
	}
	s.mu.Lock()
	pu.uploaded = true
	pu.uri = uri
	pu.gotDigest = "sha256:" + hex.EncodeToString(h.Sum(nil))
	pu.gotSize = size
	s.mu.Unlock()
	return nil
}

// FinalizeUpload verifies the uploaded object against the client-declared digest/size and
// creates the immutable artifact row (§05.6). Digest sources, in order: the digest computed
// during a stream-through upload; the backend's Stat (x-amz-meta-sha256); else a one-time
// verify-by-stream when the client declared a digest the backend can't attest cheaply.
func (s *Service) FinalizeUpload(ctx context.Context, actor, model, version, uploadID, declaredDigest string, parts []domain.MultipartPart) (*domain.Artifact, error) {
	start := time.Now()
	pu, err := s.takePending(uploadID, true)
	if err != nil {
		return nil, err
	}
	if pu.model != model || pu.version != version {
		return nil, domain.Invalid("upload '" + uploadID + "' does not belong to " + model + "@" + version)
	}
	b := s.backend(pu.backend)
	if b == nil {
		return nil, domain.Internal("upload backend '" + pu.backend + "' unavailable")
	}

	// Assemble a multipart upload from the client-observed part ETags before verifying.
	if pu.multipart {
		if len(parts) == 0 {
			_ = b.AbortMultipart(ctx, pu.path, pu.backendUploadID)
			return nil, domain.Invalid("multipart finalize requires the per-part ETags")
		}
		if _, err := b.CompleteMultipart(ctx, pu.path, pu.backendUploadID, parts); err != nil {
			return nil, err
		}
	}

	var digest string
	var size int64
	if pu.uploaded {
		// Stream-through: we hashed the bytes as they passed through.
		digest, size = pu.gotDigest, pu.gotSize
	} else {
		// Direct signed PUT: confirm the object exists and get its size.
		info, err := b.Stat(ctx, pu.uri)
		if err != nil {
			return nil, err
		}
		if !info.Exists {
			return nil, domain.Unprocessable("no object was uploaded for '" + uploadID + "'")
		}
		size = info.SizeBytes
		switch {
		case info.Digest != "":
			// The backend attests the content hash (e.g. our x-amz-meta-sha256).
			digest = info.Digest
		case declaredDigest != "" && !pu.multipart && size > 0 && size <= streamVerifyCap:
			// Small single PUT the backend can't attest: verify once by streaming it back.
			computed, err := streamDigest(ctx, b, pu.uri)
			if err != nil {
				return nil, err
			}
			digest = computed
		}
		// Larger/multipart objects: re-downloading to hash is untenable, and the object store
		// already guarantees per-part integrity. We record the client-declared digest and
		// rely on the size check below (§05.6).
	}

	if declaredDigest != "" && digest != "" && declaredDigest != digest {
		s.meter.UploadFinalized(time.Since(start).Seconds(), true)
		return nil, domain.Unprocessable("digest mismatch: declared " + declaredDigest + " but object is " + digest)
	}
	if pu.in.SizeBytes > 0 && size > 0 && pu.in.SizeBytes != size {
		return nil, domain.Unprocessable("size mismatch: declared bytes differ from uploaded object")
	}
	if digest == "" {
		digest = declaredDigest
	}

	a, err := s.registerArtifact(ctx, pu.versionID, ArtifactInput{
		Kind: pu.in.Kind, Name: pu.in.Name, URI: pu.uri, StorageBackend: pu.backend,
		StoragePath: pu.path, Digest: digest, SizeBytes: size, MediaType: pu.in.MediaType,
		ModelFormat: pu.in.ModelFormat, ServiceAccount: pu.in.ServiceAccount,
	})
	if err != nil {
		return nil, err
	}
	s.audit(ctx, actor, "artifact.upload", "artifact", a.ID, "uploaded "+model+"@"+version+"/"+a.Name, nil)
	s.events.Publish(domain.Event{Type: "artifact.created", Model: model, Version: version})
	s.meter.UploadFinalized(time.Since(start).Seconds(), false)
	return a, nil
}

// RegisterArtifact adds a by-reference artifact to an existing version (§05.5). The bytes
// already live in a backend; Stat fills any missing digest/size.
func (s *Service) RegisterArtifact(ctx context.Context, actor, model, version string, in ArtifactInput) (*domain.Artifact, error) {
	v, err := s.store.GetVersion(ctx, model, version)
	if err != nil {
		return nil, err
	}
	a, err := s.registerArtifact(ctx, v.ID, in)
	if err != nil {
		return nil, err
	}
	s.audit(ctx, actor, "artifact.register", "artifact", a.ID, "registered "+model+"@"+version+"/"+a.Name, nil)
	s.events.Publish(domain.Event{Type: "artifact.created", Model: model, Version: version})
	return a, nil
}

// takePending fetches a pending upload; if remove is true it is consumed (finalize).
func (s *Service) takePending(id string, remove bool) (*pendingUpload, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pu, ok := s.pending[id]
	if !ok {
		return nil, domain.NotFound("unknown or expired upload '" + id + "'")
	}
	if pu.expiresAt < domain.NowMillis() {
		delete(s.pending, id)
		return nil, domain.Precondition("upload '"+id+"' has expired", nil)
	}
	if remove {
		delete(s.pending, id)
	}
	return pu, nil
}

// streamDigest reads an object back through the backend to compute its sha256 — the
// integrity fallback when a signing backend can't attest the content hash itself (§05.6).
func streamDigest(ctx context.Context, b domain.StorageBackend, uri string) (string, error) {
	rc, err := b.Get(ctx, uri)
	if err != nil {
		return "", err
	}
	defer rc.Close()
	h := sha256.New()
	if _, err := io.Copy(h, rc); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}
