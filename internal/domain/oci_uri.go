package domain

import "strings"

// OCIRef is a parsed oci:// reference (§05.3.1) — the pointer form an `oci` artifact row
// carries in `uri`.
//
//	oci://<registry>[:port]/<repository>[:<tag>][@sha256:<hex>][#<layer title>]
//
// The `#fragment` names one layer inside the manifest by its
// `org.opencontainers.image.title` annotation, mirroring `lineage://…#artifact` (§04.5).
// Without a fragment the ref addresses the whole image — which is what KServe modelcars
// and any `docker pull` consume.
type OCIRef struct {
	Registry   string // host[:port]
	Repository string // path after the registry, e.g. "models/fraud-detector"
	Tag        string // optional; empty when addressed purely by digest
	Digest     string // optional; "sha256:<64 hex>" — the *manifest* digest
	Layer      string // optional; org.opencontainers.image.title of one layer
}

// Image returns the ref without its layer fragment: the pullable image reference. Digest
// wins over tag when both are present, because that is the immutable one.
func (r OCIRef) Image() string {
	s := "oci://" + r.Registry + "/" + r.Repository
	if r.Digest != "" {
		return s + "@" + r.Digest
	}
	if r.Tag != "" {
		return s + ":" + r.Tag
	}
	return s
}

// String returns the full canonical ref, layer fragment included.
func (r OCIRef) String() string {
	if r.Layer != "" {
		return r.Image() + "#" + r.Layer
	}
	return r.Image()
}

// Reference is the registry-API reference for manifest paths (/v2/<repo>/manifests/<ref>):
// the digest when pinned, else the tag.
func (r OCIRef) Reference() string {
	if r.Digest != "" {
		return r.Digest
	}
	return r.Tag
}

// ParseOCIURI parses an oci:// URI. The optional parts are stripped in a fixed order —
// fragment, then digest, then tag — so a tag containing no ':' can never be confused with
// the registry port.
func ParseOCIURI(s string) (OCIRef, error) {
	rest, ok := strings.CutPrefix(s, "oci://")
	if !ok {
		return OCIRef{}, Invalid("not an oci:// uri: '" + s + "'")
	}
	var ref OCIRef
	if before, frag, ok := strings.Cut(rest, "#"); ok {
		if frag == "" {
			return OCIRef{}, Invalid("empty layer fragment in '" + s + "'")
		}
		rest, ref.Layer = before, frag
	}
	if before, dig, ok := strings.Cut(rest, "@"); ok {
		if !ValidDigest(dig) {
			return OCIRef{}, Invalid("invalid digest in oci uri: '" + s + "'")
		}
		rest, ref.Digest = before, dig
	}
	// The registry is always the first segment — the oci:// scheme removes the ambiguity
	// docker has to resolve with a "contains a dot or colon" heuristic.
	reg, repo, ok := strings.Cut(rest, "/")
	if !ok || reg == "" || repo == "" {
		return OCIRef{}, Invalid("oci uri needs <registry>/<repository>: '" + s + "'")
	}
	ref.Registry = reg
	// A ':' after the last '/' is the tag separator; anything before belongs to the path.
	if i := strings.LastIndex(repo, ":"); i >= 0 && !strings.Contains(repo[i:], "/") {
		ref.Tag, repo = repo[i+1:], repo[:i]
		if ref.Tag == "" {
			return OCIRef{}, Invalid("empty tag in oci uri: '" + s + "'")
		}
	}
	ref.Repository = repo
	if !ValidOCIRepository(ref.Repository) {
		return OCIRef{}, Invalid("invalid repository in oci uri: '" + s + "'")
	}
	if ref.Tag != "" && !validOCITag(ref.Tag) {
		return OCIRef{}, Invalid("invalid tag in oci uri: '" + s + "'")
	}
	if ref.Tag == "" && ref.Digest == "" {
		return OCIRef{}, Invalid("oci uri needs a tag or a digest: '" + s + "'")
	}
	if ref.Layer != "" && !ValidArtifactName(ref.Layer) {
		return OCIRef{}, Invalid("invalid layer fragment in oci uri: '" + s + "'")
	}
	return ref, nil
}

// ValidDigest reports whether s is a "sha256:<64 lowercase hex>" content digest — the only
// algorithm Lineage records (§05.5).
func ValidDigest(s string) bool {
	hex, ok := strings.CutPrefix(s, "sha256:")
	if !ok || len(hex) != 64 {
		return false
	}
	for i := 0; i < len(hex); i++ {
		c := hex[i]
		if !(c >= '0' && c <= '9') && !(c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// ValidOCIRepository applies the distribution-spec name grammar: slash-separated components
// of lowercase alphanumerics with single separators (., _, __, -) between them.
func ValidOCIRepository(s string) bool {
	if s == "" || len(s) > 255 {
		return false
	}
	for comp := range strings.SplitSeq(s, "/") {
		if !validRepoComponent(comp) {
			return false
		}
	}
	return true
}

func validRepoComponent(c string) bool {
	if c == "" {
		return false
	}
	prevSep := true // a component may not start with a separator
	for i := 0; i < len(c); i++ {
		ch := c[i]
		switch {
		case (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9'):
			prevSep = false
		case ch == '.' || ch == '_' || ch == '-':
			// Separators may not lead, trail, or (except for "__" and "-+") repeat.
			if prevSep && !(ch == '_' && i > 0 && c[i-1] == '_') && !(ch == '-' && i > 0 && c[i-1] == '-') {
				return false
			}
			prevSep = true
		default:
			return false
		}
	}
	return !prevSep
}

// validOCITag applies the distribution-spec tag grammar: up to 128 chars of word characters,
// dots and dashes, not leading with a dot or dash.
func validOCITag(s string) bool {
	if s == "" || len(s) > 128 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		alnum := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
		switch {
		case alnum || c == '_':
		case c == '.' || c == '-':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// OCITag sanitizes a version name into a legal OCI tag (§05.3.1). Version names are already
// constrained by ValidName, so this only has to cover the leading-character rule.
func OCITag(version string) string {
	if version == "" {
		return "latest"
	}
	var b strings.Builder
	for i := 0; i < len(version) && b.Len() < 128; i++ {
		c := version[i]
		alnum := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
		switch {
		case alnum || c == '_' || c == '.' || c == '-':
			if b.Len() == 0 && (c == '.' || c == '-') {
				b.WriteByte('_')
				continue
			}
			b.WriteByte(c)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "latest"
	}
	return b.String()
}
