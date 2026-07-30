# CLAUDE.md

Guidance for Claude Code (and humans) working in this repository.

## What this project is

**Lineage** is a feature-complete, self-hostable **AI model registry** — the system
of record for ML/AI models across their lifecycle. It is the metadata + governance
layer between experimentation and production: it knows what models and versions
exist, where their artifacts live, what lifecycle stage each is in, who owns them,
and how consumers fetch them.

We benchmark against **Kubeflow Model Registry** and aim for **capability parity,
not API/wire compatibility** — we match what it can do, with our own cleaner data
model and API.

Start with `docs/00-preplanning.md` for the full framing, axioms, and decisions.

## Core axioms (do not violate without an explicit decision)

1. **Self-hosting is the primary distribution model.** SaaS never compromises the
   self-hosted experience.
2. **Helm is a first-class product surface**, versioned and tested with the code.
   "One `helm install` yields a working, secure registry" is an acceptance
   criterion. No Istio dependency.
3. **Single binary, two surfaces on two ports.** One Go binary / one Deployment. It
   serves the **Admin UI** (human web console, `:8080`) and the **Model API**
   (machine-facing: publish + resolve + fetch, `:8081`, `/v1`). The port is the surface
   selector (no `/admin` prefix). **Publishing is a Model API operation, not Admin —
   Admin is UI/human interaction only.** No separate deployables in v1.
4. **Auth is out of scope.** Infra (ingress/gateway/mesh/NetworkPolicy) owns
   authN/authZ. Lineage trusts already-authenticated requests and only records an
   infra-provided identity header (e.g. `X-Lineage-Actor`) for audit attribution.
5. **Capability parity with Kubeflow, our own interface** (no MLMD, no wire compat).
6. **Storage-agnostic, metadata-authoritative.** Lineage owns metadata + pointers;
   artifact bytes live in pluggable backends.
7. **Auditable by default.** Every state transition is recorded.
8. **Frictionless for inference systems.** KServe, Modal, Baseten et al. must consume
   from the Model API with near-zero glue: resolution returns native `storageUri`s
   (`s3://`/`gs://`/`oci://`/`hf://`), signed HTTPS URLs, digest, size, and model
   format. Ship an optional KServe `lineage://<model>/<stage>` storage-initializer.

## Locked technical decisions (v1)

- **Language/stack:** Go.
- **API contract:** REST + OpenAPI (OpenAPI spec is the source of truth; SDK/CLI
  generated from it). Python SDK first. gRPC deferred.
- **Artifacts:** pluggable blob storage first (S3/GCS/Azure/FS) with signed-URL
  delivery (stream-through fallback); **OCI/ORAS driver added later**, not v1.
- **Tenancy:** single-tenant per install for v1. No Project/Namespace entity yet;
  reserve scope keys so multi-tenancy is an additive change later.
- **Metadata store:** SQLite **and** Postgres, both supported. SQLite for
  zero-dependency dev/demo/small installs; Postgres for HA/prod. One schema via a
  portable data-access layer — no engine-specific SQL. Selected via config.

## Documentation conventions — MANDATORY

These are hard rules. Follow them every time without being reminded.

1. **All docs live in `docs/` and are numbered.** Every architecture/instruction
   doc filename begins with a zero-padded ordinal prefix reflecting reading order:
   `00-preplanning.md`, `01-architecture-overview.md`, `02-data-model.md`, …
   ADRs live in `docs/ADRs/` and are likewise numbered (`0001-<slug>.md`). Never add
   an un-numbered doc to `docs/`.

2. **All diagrams are Mermaid.** Every diagram — architecture, sequence, ER, state
   machine, flow — MUST be authored as a fenced ```mermaid block. Do **not** use
   ASCII-art diagrams, external image files, or screenshots for diagrams. If you
   find an existing ASCII diagram, convert it to Mermaid when you touch that doc.

3. **Be concise and on point.** Docs use the minimum words needed to be precise.
   No filler, no marketing prose, no restating the obvious. Prefer tables, lists,
   and diagrams over paragraphs. Every sentence should carry information a reader
   needs. Cut ruthlessly.

## Working style here

- This is a docs-first project right now: we write architecture before code.
- When a decision in `docs/00-preplanning.md` §11 is resolved, record it as a
  numbered ADR under `docs/ADRs/` and update the preplanning doc.
- Keep the `docs/` roadmap (preplanning §10) in sync as docs are added.
