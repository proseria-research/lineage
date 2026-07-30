package s3

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

// AWS's published "GET Object" presigned-URL example from the SigV4 documentation.
// Fixed inputs => a fixed, externally-verifiable signature. This pins our canonicalization
// and signing-key derivation to the reference implementation.
// https://docs.aws.amazon.com/AmazonS3/latest/API/sigv4-query-string-auth.html
func TestPresignGetObjectVector(t *testing.T) {
	s := signer{region: "us-east-1"}
	cr := creds{accessKey: "AKIAIOSFODNN7EXAMPLE", secretKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"}
	now := time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC)

	got := s.presign(cr, "GET", "https", "examplebucket.s3.amazonaws.com", "/test.txt", url.Values{}, 86400*time.Second, now)

	const wantSig = "aeeed9bbccd4d02ee5c0109b86d86835f995330da4c265957d157751f604d404"
	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("parse presigned url: %v", err)
	}
	if sig := u.Query().Get("X-Amz-Signature"); sig != wantSig {
		t.Fatalf("signature mismatch:\n got  %s\n want %s\n url  %s", sig, wantSig, got)
	}
	if cred := u.Query().Get("X-Amz-Credential"); cred != "AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request" {
		t.Fatalf("bad credential scope: %s", cred)
	}
	if !strings.HasPrefix(got, "https://examplebucket.s3.amazonaws.com/test.txt?") {
		t.Fatalf("bad url base: %s", got)
	}
}

func TestURIEncode(t *testing.T) {
	cases := map[string]string{
		"a/b c":      "a/b%20c",  // space encoded, slash preserved
		"tilde~ok":   "tilde~ok", // unreserved
		"plus+eq=":   "plus%2Beq%3D",
		"model.onnx": "model.onnx",
	}
	for in, want := range cases {
		if got := uriEncode(in, false); got != want {
			t.Errorf("uriEncode(%q)=%q want %q", in, got, want)
		}
	}
	if got := uriEncode("a/b", true); got != "a%2Fb" {
		t.Errorf("encodeSlash=true should encode '/', got %q", got)
	}
}
