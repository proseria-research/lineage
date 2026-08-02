package domain

import "testing"

func TestParseOCIURI(t *testing.T) {
	const dig = "sha256:" + "ab" + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcd"

	cases := []struct {
		name string
		in   string
		want OCIRef
	}{
		{"tag", "oci://ghcr.io/acme/models:v1", OCIRef{Registry: "ghcr.io", Repository: "acme/models", Tag: "v1"}},
		{"tag and layer", "oci://ghcr.io/acme/models:v1#model.onnx",
			OCIRef{Registry: "ghcr.io", Repository: "acme/models", Tag: "v1", Layer: "model.onnx"}},
		{"digest only", "oci://ghcr.io/acme/models@" + dig,
			OCIRef{Registry: "ghcr.io", Repository: "acme/models", Digest: dig}},
		{"tag and digest", "oci://ghcr.io/acme/models:v1@" + dig,
			OCIRef{Registry: "ghcr.io", Repository: "acme/models", Tag: "v1", Digest: dig}},
		// The registry port must not be mistaken for a tag separator.
		{"registry port", "oci://registry:5000/models/fraud:2024.01",
			OCIRef{Registry: "registry:5000", Repository: "models/fraud", Tag: "2024.01"}},
		{"single-segment repo", "oci://localhost:5000/fraud:v1",
			OCIRef{Registry: "localhost:5000", Repository: "fraud", Tag: "v1"}},
		{"underscore run is legal", "oci://ghcr.io/a__b:v1",
			OCIRef{Registry: "ghcr.io", Repository: "a__b", Tag: "v1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseOCIURI(tc.in)
			if err != nil {
				t.Fatalf("ParseOCIURI(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("ParseOCIURI(%q) = %+v, want %+v", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseOCIURIRejects(t *testing.T) {
	bad := []struct{ name, in string }{
		{"wrong scheme", "s3://bucket/key"},
		{"no repository", "oci://ghcr.io"},
		{"no tag or digest", "oci://ghcr.io/acme/models"},
		{"empty tag", "oci://ghcr.io/acme/models:"},
		{"empty fragment", "oci://ghcr.io/acme/models:v1#"},
		{"bad digest algorithm", "oci://ghcr.io/acme/models@md5:abc"},
		{"short digest", "oci://ghcr.io/acme/models@sha256:abcd"},
		{"uppercase repository", "oci://ghcr.io/Acme/models:v1"},
		{"dot run in repository", "oci://ghcr.io/acme..models:v1"},
		{"leading separator in repository", "oci://ghcr.io/.acme:v1"},
		{"tag leads with a dot", "oci://ghcr.io/acme:.v1"},
		{"traversal fragment", "oci://ghcr.io/acme:v1#.."},
		{"path in fragment", "oci://ghcr.io/acme:v1#a/b"},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			if got, err := ParseOCIURI(tc.in); err == nil {
				t.Fatalf("ParseOCIURI(%q) accepted %+v, want an error", tc.in, got)
			}
		})
	}
}

func TestOCIRefRoundTrip(t *testing.T) {
	for _, in := range []string{
		"oci://ghcr.io/acme/models:v1",
		"oci://ghcr.io/acme/models:v1#model.onnx",
		"oci://registry:5000/m/fraud@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	} {
		ref, err := ParseOCIURI(in)
		if err != nil {
			t.Fatalf("ParseOCIURI(%q): %v", in, err)
		}
		if got := ref.String(); got != in {
			t.Fatalf("round trip: got %q, want %q", got, in)
		}
	}
}

// Image() drops the layer fragment — that reference is what a consumer pulls, and it is what
// the resolve response hands to KServe as a storageUri (§04.6).
func TestOCIRefImageDropsFragment(t *testing.T) {
	ref, err := ParseOCIURI("oci://ghcr.io/acme/models:v1#model.onnx")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ref.Image(), "oci://ghcr.io/acme/models:v1"; got != want {
		t.Fatalf("Image() = %q, want %q", got, want)
	}
	if got, want := ref.Reference(), "v1"; got != want {
		t.Fatalf("Reference() = %q, want %q", got, want)
	}
}

// A digest pins the manifest, so it wins over the tag wherever one reference is needed.
func TestOCIRefDigestWinsOverTag(t *testing.T) {
	const dig = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	ref := OCIRef{Registry: "ghcr.io", Repository: "acme", Tag: "v1", Digest: dig}
	if got, want := ref.Reference(), dig; got != want {
		t.Fatalf("Reference() = %q, want %q", got, want)
	}
	if got, want := ref.Image(), "oci://ghcr.io/acme@"+dig; got != want {
		t.Fatalf("Image() = %q, want %q", got, want)
	}
}

func TestOCITag(t *testing.T) {
	cases := map[string]string{
		"v1":      "v1",
		"1.0.0":   "1.0.0",
		"2024-01": "2024-01",
		"":        "latest",
		// A version name may legally lead with a character a tag may not.
		".hidden": "_hidden",
		"-lead":   "_lead",
	}
	for in, want := range cases {
		if got := OCITag(in); got != want {
			t.Fatalf("OCITag(%q) = %q, want %q", in, got, want)
		}
		if !validOCITag(OCITag(in)) {
			t.Fatalf("OCITag(%q) = %q, which is not a legal tag", in, OCITag(in))
		}
	}
}

func TestValidDigest(t *testing.T) {
	good := "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if !ValidDigest(good) {
		t.Fatal("rejected a well-formed digest")
	}
	for _, bad := range []string{
		"", "sha256:", "sha512:" + good[7:], good + "0", good[:len(good)-1],
		"sha256:0123456789ABCDEF0123456789abcdef0123456789abcdef0123456789abcdef", // uppercase hex
		"sha256:0123456789abcdefg123456789abcdef0123456789abcdef0123456789abcdef", // 'g'
	} {
		if ValidDigest(bad) {
			t.Fatalf("accepted malformed digest %q", bad)
		}
	}
}
