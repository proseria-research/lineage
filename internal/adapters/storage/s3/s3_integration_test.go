package s3

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/proseria-research/lineage/internal/domain"
)

// Live round-trip against a real S3-compatible endpoint. Skipped unless configured, so CI
// stays hermetic. To run against MinIO:
//
//	docker run -p9000:9000 -e MINIO_ROOT_USER=minioadmin -e MINIO_ROOT_PASSWORD=minioadmin minio/minio server /data
//	(create bucket "lineage-test", then:)
//	LINEAGE_TEST_S3_ENDPOINT=http://localhost:9000 LINEAGE_TEST_S3_BUCKET=lineage-test \
//	LINEAGE_TEST_S3_KEY=minioadmin LINEAGE_TEST_S3_SECRET=minioadmin LINEAGE_TEST_S3_PATH_STYLE=1 \
//	go test ./internal/adapters/storage/s3/ -run Integration -v
func TestS3IntegrationRoundTrip(t *testing.T) {
	bucket := os.Getenv("LINEAGE_TEST_S3_BUCKET")
	if bucket == "" {
		t.Skip("set LINEAGE_TEST_S3_BUCKET (+ endpoint/key/secret) to run the live S3 test")
	}
	b, err := New("it", Config{
		Bucket:    bucket,
		Region:    envOr("LINEAGE_TEST_S3_REGION", "us-east-1"),
		Endpoint:  os.Getenv("LINEAGE_TEST_S3_ENDPOINT"),
		AccessKey: os.Getenv("LINEAGE_TEST_S3_KEY"),
		SecretKey: os.Getenv("LINEAGE_TEST_S3_SECRET"),
		PathStyle: os.Getenv("LINEAGE_TEST_S3_PATH_STYLE") != "",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	key := "lineage-it/obj-" + hex.EncodeToString([]byte(time.Now().Format("150405")))
	payload := []byte("s3 integration payload \x00\x01\x02")
	sum := sha256.Sum256(payload)
	wantDigest := "sha256:" + hex.EncodeToString(sum[:])

	// Server-side Put (also exercises header-signed PUT + x-amz-meta-sha256).
	uri, err := b.Put(ctx, key, bytes.NewReader(payload), int64(len(payload)), "application/octet-stream")
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	t.Cleanup(func() { _ = b.Delete(ctx, uri) })

	// Stat surfaces size + the sha256 we tagged.
	info, err := b.Stat(ctx, uri)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if !info.Exists || info.SizeBytes != int64(len(payload)) || info.Digest != wantDigest {
		t.Fatalf("Stat = %+v, want exists,size=%d,digest=%s", info, len(payload), wantDigest)
	}

	// Presigned GET works from a plain HTTP client (no credentials on the wire).
	signed, err := b.SignGet(ctx, uri, time.Minute)
	if err != nil {
		t.Fatalf("SignGet: %v", err)
	}
	resp, err := http.Get(signed)
	if err != nil {
		t.Fatalf("GET presigned: %v", err)
	}
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !bytes.Equal(got, payload) {
		t.Fatalf("presigned GET: status=%d body=%q", resp.StatusCode, got)
	}

	// Presigned PUT: upload a second object directly via the signed URL.
	sr, err := b.SignPut(ctx, key+"-2", time.Minute)
	if err != nil {
		t.Fatalf("SignPut: %v", err)
	}
	req, _ := http.NewRequest(sr.Method, sr.URL, bytes.NewReader(payload))
	preq, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT presigned: %v", err)
	}
	preq.Body.Close()
	if preq.StatusCode != 200 {
		t.Fatalf("presigned PUT status=%d", preq.StatusCode)
	}
	t.Cleanup(func() { _ = b.Delete(ctx, uri+"-2") })

	// ListObjects sees the object we wrote.
	refs, err := b.ListObjects(ctx, "s3://"+bucket+"/lineage-it/")
	if err != nil {
		t.Fatalf("ListObjects: %v", err)
	}
	if len(refs) == 0 {
		t.Fatalf("ListObjects returned nothing under lineage-it/")
	}
}

// Live multipart round-trip: initiate → PUT each part via its presigned URL → complete.
func TestS3IntegrationMultipart(t *testing.T) {
	bucket := os.Getenv("LINEAGE_TEST_S3_BUCKET")
	if bucket == "" {
		t.Skip("set LINEAGE_TEST_S3_BUCKET (+ endpoint/key/secret) to run the live S3 test")
	}
	b, err := New("it", Config{
		Bucket: bucket, Region: envOr("LINEAGE_TEST_S3_REGION", "us-east-1"),
		Endpoint: os.Getenv("LINEAGE_TEST_S3_ENDPOINT"), AccessKey: os.Getenv("LINEAGE_TEST_S3_KEY"),
		SecretKey: os.Getenv("LINEAGE_TEST_S3_SECRET"), PathStyle: os.Getenv("LINEAGE_TEST_S3_PATH_STYLE") != "",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	key := "lineage-it/mpu-" + hex.EncodeToString([]byte(time.Now().Format("150405")))

	const partSize = 5 << 20 // S3 minimum part size
	plan, err := b.InitiateMultipart(ctx, key, 2, partSize, 10*time.Minute)
	if err != nil {
		t.Fatalf("InitiateMultipart: %v", err)
	}
	t.Cleanup(func() { _ = b.Delete(ctx, "s3://"+bucket+"/"+key) })

	part := bytes.Repeat([]byte("x"), partSize)
	done := make([]domain.MultipartPart, 0, 2)
	for _, p := range plan.Parts {
		req, _ := http.NewRequest("PUT", p.URL, bytes.NewReader(part))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("PUT part %d: %v", p.PartNumber, err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("PUT part %d status=%d", p.PartNumber, resp.StatusCode)
		}
		done = append(done, domain.MultipartPart{PartNumber: p.PartNumber, ETag: resp.Header.Get("ETag")})
	}
	if _, err := b.CompleteMultipart(ctx, key, plan.UploadID, done); err != nil {
		t.Fatalf("CompleteMultipart: %v", err)
	}
	info, err := b.Stat(ctx, "s3://"+bucket+"/"+key)
	if err != nil || !info.Exists || info.SizeBytes != int64(2*partSize) {
		t.Fatalf("Stat after multipart = %+v err=%v, want size %d", info, err, 2*partSize)
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
