# 05 — Storage & Artifacts

> Status: **Draft**. The `StorageBackend` port, its drivers, artifact addressing,
> content integrity, signed upload/download, stream-through fallback, and garbage
> collection. Metadata is `02`; the APIs that call this are `03` (upload) and `04`
> (fetch).

## 1. Principle

Lineage is **metadata-authoritative, storage-agnostic** (§00 axiom 6). It owns the
`artifact` row and its pointer; **bytes live in a pluggable backend** and, on the read
path, **flow directly between the backend and the consumer** — the core never proxies
them unless a backend can't sign.

## 2. The `StorageBackend` Port

One interface, many drivers, wired at startup (§01). The core depends only on this.

| Method | Purpose |
|---|---|
| `Stat(uri) → {exists, sizeBytes, digest?}` | verify/fill metadata on register & finalize |
| `SignGet(uri, ttl) → signedUrl` | mint a time-limited download URL (if capable) |
| `SignPut(path, ttl) → {url, method, headers}` | mint a direct-upload target (if capable) |
| `Get(uri) → stream` | stream-through read (fallback) |
| `Put(path, reader) → uri` | server-side write (optional; small/DOC artifacts) |
| `Delete(uri)` | remove object (GC) |
| `Capabilities() → {signing, ranges, multipart, oci}` | feature probe |

The API adapts to `Capabilities()`: no `signing` ⇒ resolve/fetch fall back to
stream-through (§04.3); no `multipart` ⇒ single-PUT uploads only.

## 3. Drivers

```mermaid
flowchart TB
    core["Domain core"] --> port["StorageBackend port"]
    port --> s3["s3<br/>(AWS · MinIO · R2 · Ceph)"]
    port --> gcs["gcs"]
    port --> az["azure blob"]
    port --> fs["fs (local / PVC)"]
    port --> oci["oci / ORAS (later)"]
    s3 --> o1[("S3 API")]
    gcs --> o2[("GCS")]
    az --> o3[("Azure")]
    fs --> o4[("Filesystem")]
    oci --> o5[("OCI registry")]
```

| Driver | URI scheme | Signing | Notes |
|---|---|---|---|
| `s3` | `s3://` | ✅ | any S3-compatible (MinIO, Cloudflare R2, Ceph) via endpoint override |
| `gcs` | `gs://` | ✅ | signed URLs via SA / workload identity |
| `azure` | `az://` | ✅ | SAS tokens |
| `fs` | `file://` | ❌ | local/PVC; uses stream-through (dev, air-gapped) |
| `oci` | `oci://…@sha256:` | n/a (pull) | **later** (§00.11.4): ORAS; enables KServe modelcars |

## 4. Configuration

The backend is **config, not rows** (v1: one active backend, env-driven). Selected by
`LINEAGE_STORAGE_DRIVER` (`fs` | `s3`); its `name` (`default`) is referenced by
`artifact.storageBackend` (`02.3.3`).

```bash
LINEAGE_STORAGE_DRIVER=s3
LINEAGE_S3_BUCKET=models
LINEAGE_S3_REGION=us-east-1
LINEAGE_S3_ENDPOINT=https://s3.amazonaws.com   # override for MinIO / R2 / Ceph
LINEAGE_S3_PATH_STYLE=true                     # required for MinIO / Ceph
# credentials: omit the two below to use the auto chain (recommended in-cluster)
LINEAGE_S3_ACCESS_KEY=…  LINEAGE_S3_SECRET_KEY=…   # pins static keys
```

### 4.1 Credential resolution

**Credentials never live in Lineage responses.** They are resolved from the environment
with least privilege. Setting the two static keys pins them; otherwise a provider **chain**
is tried, first non-empty wins, and **temporary credentials are cached and refreshed 5 min
before expiry** so signing never races an expiring token:

| Order | Source | Trigger |
|---|---|---|
| 1 | static keys | `LINEAGE_S3_ACCESS_KEY` + `_SECRET_KEY` set |
| 2 | env | `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY` [/ `AWS_SESSION_TOKEN`] |
| 3 | **IRSA / EKS Pod Identity** | `AWS_ROLE_ARN` + `AWS_WEB_IDENTITY_TOKEN_FILE` → STS `AssumeRoleWithWebIdentity` |
| 4 | ECS task role | `AWS_CONTAINER_CREDENTIALS_{RELATIVE,FULL}_URI` |
| 5 | EC2 instance role | IMDSv2 |

The in-cluster `serviceAccount` hint on a `MODEL` artifact (`02.3.3`) is *for the serving
system's* pull, not for Lineage.

## 5. Addressing & Integrity

- **Native URIs** are canonical (`artifact.uri`), so consumers pull with their existing
  tooling. Optional path convention when Lineage writes:
  `<root>/<model>/<version>/<artifactName>` (`storagePath`).
- **Digests are first-class.** `digest = sha256:…` is the identity and the `ETag`
  (§04). Computed at upload finalize, or via `Stat` at register-by-reference.
- **Immutable once set** (`02.5`): a digest'd artifact's bytes/pointer never change; new
  content ⇒ new version. This makes artifacts safely cacheable and reproducible.

## 6. Upload (finalize verifies)

Expands §03.6. Bytes bypass the core entirely when the backend can sign.

```mermaid
sequenceDiagram
    participant C as CI / SDK
    participant A as Model API
    participant B as StorageBackend
    participant OBJ as Object store

    C->>A: POST …/artifacts:initiateUpload {name, kind, sizeBytes?}
    A->>B: SignPut(path) [+ multipart plan if large]
    A-->>C: {uploadId, url(s), method, headers, expiresAt}
    C->>OBJ: PUT bytes (direct)
    OBJ-->>C: 200 (ETag)
    C->>A: POST …/artifacts:finalizeUpload {uploadId, digest}
    A->>B: Stat(uri) — verify size + digest
    alt matches
        A-->>C: 201 Artifact (immutable)
    else mismatch
        A-->>C: 422 unprocessable (digest/size mismatch)
    end
```

- **Large files:** if `Capabilities().multipart`, `initiateUpload` returns a multipart
  plan (part URLs); `finalizeUpload` completes it. Otherwise a single signed PUT.
- **Small / DOC artifacts** may use server-side `Put` (streamed through the API) for
  convenience.

## 7. Delivery Decision

```mermaid
flowchart TD
    q{Backend can sign?} -- yes --> s["return storageUri + fresh signedUrl<br/>(consumer pulls directly)"]
    q -- no --> t["stream-through /content<br/>(fs, air-gapped) — Range supported"]
```

Signed-URL is always preferred (offloads bytes, scales the resolve path). Stream-through
is a correctness fallback, not the default.

## 8. Garbage Collection

- **Default = retain.** `DELETE` on an artifact removes the **metadata row**, not the
  bytes — safe, and bytes may be shared/referenced.
- **Optional GC** (`LINEAGE_STORAGE_GC=retain|sweep`): a periodic sweeper (`ListObjects` +
  `ArtifactRefsURI`) deletes backend objects with **no** referencing artifact row
  (reference-counted by `uri`), honoring `LINEAGE_GC_GRACE`. Path-scoped to
  `LINEAGE_GC_PREFIX` so Lineage never deletes objects it didn't write.

## 9. See Also

| For | Doc |
|---|---|
| Artifact schema, immutability, digests | `02` |
| Upload API surface | `03` |
| Resolve/fetch, signed-URL delivery, `lineage://` | `04` |
| Backend credential mounting, PVC for `fs` | `08` |
| Storage metrics (signing, upload latency, errors) | `09` |
