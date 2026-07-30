package s3

// S3 multipart upload (§05.6) and object listing (§05.8, GC). Multipart lets a client PUT a
// large object in parallel parts straight to object storage: the backend initiates the
// upload and hands back a presigned PUT URL per part; the client uploads each and reports
// its ETag; the backend completes the upload server-side.

import (
	"context"
	"encoding/xml"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/proseria-research/lineage/internal/domain"
)

// InitiateMultipart creates an S3 multipart upload and returns presigned PUT URLs per part.
func (b *Backend) InitiateMultipart(ctx context.Context, path string, parts int, partSize int64, ttl time.Duration) (domain.MultipartPlan, error) {
	key := strings.TrimPrefix(path, "/")
	q := url.Values{"uploads": {""}}
	resp, err := b.do(ctx, "POST", b.cfg.Bucket, key, q, emptyHash, nil, nil)
	if err != nil {
		return domain.MultipartPlan{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return domain.MultipartPlan{}, b.httpErr("initiate-multipart", resp)
	}
	var res struct {
		UploadID string `xml:"UploadId"`
	}
	if err := xml.NewDecoder(resp.Body).Decode(&res); err != nil {
		return domain.MultipartPlan{}, domain.Internal("s3 multipart: parse initiate: " + err.Error())
	}

	cr, err := b.creds.resolve(ctx)
	if err != nil {
		return domain.MultipartPlan{}, err
	}
	host, canonicalURI := b.endpoint(b.cfg.Bucket, key)
	now := time.Now()
	plan := domain.MultipartPlan{UploadID: res.UploadID, PartSize: partSize, Parts: make([]domain.MultipartPart, parts)}
	for i := range parts {
		pq := url.Values{"partNumber": {strconv.Itoa(i + 1)}, "uploadId": {res.UploadID}}
		u := b.sign.presign(cr, "PUT", b.scheme, host, canonicalURI, pq, ttl, now)
		plan.Parts[i] = domain.MultipartPart{PartNumber: i + 1, URL: u}
	}
	return plan, nil
}

// CompleteMultipart assembles the object from the client-reported part ETags (any order;
// sorted here by part number) and returns the canonical native uri.
func (b *Backend) CompleteMultipart(ctx context.Context, path, uploadID string, parts []domain.MultipartPart) (string, error) {
	key := strings.TrimPrefix(path, "/")
	ordered := append([]domain.MultipartPart(nil), parts...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].PartNumber < ordered[j].PartNumber })

	type part struct {
		PartNumber int    `xml:"PartNumber"`
		ETag       string `xml:"ETag"`
	}
	doc := struct {
		XMLName xml.Name `xml:"CompleteMultipartUpload"`
		Parts   []part   `xml:"Part"`
	}{}
	for _, p := range ordered {
		doc.Parts = append(doc.Parts, part{PartNumber: p.PartNumber, ETag: p.ETag})
	}
	body, err := xml.Marshal(doc)
	if err != nil {
		return "", domain.Internal("s3 multipart: marshal complete: " + err.Error())
	}
	q := url.Values{"uploadId": {uploadID}}
	resp, err := b.do(ctx, "POST", b.cfg.Bucket, key, q, hexSHA256(body), body, nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", b.httpErr("complete-multipart", resp)
	}
	// S3 can return 200 with an <Error> body if completion actually failed.
	var probe struct {
		XMLName xml.Name
		Code    string `xml:"Code"`
		Message string `xml:"Message"`
	}
	if xml.NewDecoder(resp.Body).Decode(&probe) == nil && probe.XMLName.Local == "Error" {
		return "", domain.Internal("s3 complete-multipart: " + probe.Code + " " + probe.Message)
	}
	return "s3://" + b.cfg.Bucket + "/" + key, nil
}

// AbortMultipart cancels an incomplete multipart upload (frees the accumulated parts).
func (b *Backend) AbortMultipart(ctx context.Context, path, uploadID string) error {
	key := strings.TrimPrefix(path, "/")
	q := url.Values{"uploadId": {uploadID}}
	resp, err := b.do(ctx, "DELETE", b.cfg.Bucket, key, q, unsignedload, nil, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 204 && resp.StatusCode != 200 {
		return b.httpErr("abort-multipart", resp)
	}
	return nil
}

// ListObjects enumerates objects under prefix via ListObjectsV2, following continuation
// tokens, returning s3://bucket/key uris with size and last-modified (§05.8, GC).
func (b *Backend) ListObjects(ctx context.Context, prefix string) ([]domain.ObjectRef, error) {
	_, keyPrefix, _ := b.parse(prefix) // tolerate s3://bucket/prefix or a bare prefix
	var out []domain.ObjectRef
	token := ""
	for {
		q := url.Values{"list-type": {"2"}}
		if keyPrefix != "" {
			q.Set("prefix", keyPrefix)
		}
		if token != "" {
			q.Set("continuation-token", token)
		}
		resp, err := b.do(ctx, "GET", b.cfg.Bucket, "", q, unsignedload, nil, nil)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != 200 {
			err := b.httpErr("list", resp)
			resp.Body.Close()
			return nil, err
		}
		var lr struct {
			Contents []struct {
				Key          string    `xml:"Key"`
				Size         int64     `xml:"Size"`
				LastModified time.Time `xml:"LastModified"`
			} `xml:"Contents"`
			IsTruncated bool   `xml:"IsTruncated"`
			NextToken   string `xml:"NextContinuationToken"`
		}
		derr := xml.NewDecoder(resp.Body).Decode(&lr)
		resp.Body.Close()
		if derr != nil {
			return nil, domain.Internal("s3 list: parse: " + derr.Error())
		}
		for _, c := range lr.Contents {
			out = append(out, domain.ObjectRef{
				URI: "s3://" + b.cfg.Bucket + "/" + c.Key, SizeBytes: c.Size,
				ModifiedAt: c.LastModified.UnixMilli(),
			})
		}
		if !lr.IsTruncated || lr.NextToken == "" {
			break
		}
		token = lr.NextToken
	}
	return out, nil
}
