# Contributing to Lineage

Thanks for helping. Issues, discussions, and pull requests are all welcome. Everyone taking
part agrees to the [Code of Conduct](CODE_OF_CONDUCT.md).

## Before you start

- **Bugs**: open an issue with the version or commit, the configuration (store, storage
  driver, cache), what you did, and what happened instead.
- **Features**: open an issue or discussion first. Lineage is docs-first: a change to
  behavior usually starts as a change to a design doc in [`docs/`](docs/).
- **Security issues**: do not open a public issue. See [Reporting security
  issues](#reporting-security-issues).

Read [`docs/00-preplanning.md`](docs/00-preplanning.md) before proposing larger changes. Its
axioms are deliberate and are only changed by an explicit decision in §11:

- Self-hosting comes first.
- One binary, with two surfaces on two ports.
- Authentication belongs to the infrastructure, not to Lineage.
- Lineage stores metadata and pointers; artifact bytes stay in pluggable storage.
- Every state transition is audited.

## Setup

| Tool | Needed for |
| --- | --- |
| Go 1.25+ | Everything |
| Node 20+ and pnpm | The admin console (`make web`, `make build`) and the website (`site/`) |
| Python 3.10+ | The Python SDK |
| Docker, Helm | Images and the chart |

```bash
make build         # console + binary with the console embedded → bin/lineage
make run           # build and start: API :8081, console :8080, ops :9090
make seed          # load demo data into the running registry
make reset         # delete local state (stop the registry first)
make web-dev       # console with hot reload
make cli           # command-line client → bin/lineage-cli
```

A plain `go build` or `go test` needs no Node. The console is embedded only under the
`console` build tag, and without it a stub is compiled in.

## Tests

```bash
make test          # go test ./...
make test-console  # the same, with the console embedded
make test-e2e      # launch the real binary and run publish → resolve end to end
make fmt vet       # gofmt + go vet
make sdk-check     # regenerate and compile the Python SDK's API manifest
make helm-lint     # lint the chart against both value profiles
make site-check    # type-check the website
```

The suite needs no external services: SQLite, an embedded Postgres, and miniredis all run
in-process.

- New behavior ships with tests.
- Store behavior belongs in the shared suite in `internal/adapters/store/storetest`, so it
  runs against every engine.
- Bug fixes include a test that fails without the fix.

## Where things go

| Change | Where |
| --- | --- |
| Business rules | `internal/core`, `internal/domain` |
| A new store, storage driver, or cache | `internal/adapters/…`, behind the existing port |
| Model API endpoints | `internal/api/modelapi`, plus `openapi.json` |
| Console | `internal/api/adminui` (BFF) and `internal/api/adminui/web` (React) |
| Chart | `deploy/helm/lineage` |
| Design | `docs/NN-*.md` |

Rules:

- **Ports and adapters.** `internal/core` and `internal/domain` depend only on port
  interfaces and never import an adapter.
- **OpenAPI is the contract.** An API change updates `internal/api/modelapi/openapi.json` in
  the same change, then `make sdk-check` regenerates the SDK manifest.
- **Both SQL engines.** SQLite and Postgres each keep their own SQL. A schema change lands in
  both, and core behavior must match on both.
- **Few dependencies.** Runtime dependencies are limited to the SQLite, Postgres, and Redis
  drivers. Adding one needs a reason stated in the PR.
- **Helm is product.** A change that affects deployment updates the chart, and
  `make helm-lint` must pass.

## Documentation

Docs follow three rules:

1. Files in `docs/` are numbered in reading order (`NN-topic.md`). There are no unnumbered
   docs and no ADRs; decisions go in [`docs/00-preplanning.md`](docs/00-preplanning.md) §11.
2. Every diagram is a ```` ```mermaid ```` block. No ASCII art or images.
3. Be concise. Prefer tables and lists to paragraphs.

User-facing guides live in [`site/`](site/) and are published at
[lineage.proseria.ca](https://lineage.proseria.ca).

## Commits and pull requests

Commit subjects follow [Conventional Commits](https://www.conventionalcommits.org/), with the
area as the scope:

```
fix(console): no stray vertical scrollbar on tab strips
feat(store): …
docs: …
```

Common scopes are `api`, `core`, `store`, `storage`, `console`, `site`, `helm`, `sdk`, and
`make`. Write the subject in the imperative, and use the body to explain why.

Before opening a PR:

1. Keep it to one focused change.
2. Run `make fmt vet test`. Also run `make test-console` if you touched the console, and
   `make helm-lint` if you touched the chart.
3. Update the design doc, `openapi.json`, and `MILESTONES.md` where relevant.
4. In the description, explain the motivation and link the issue or design doc.

## Reporting security issues

Report vulnerabilities privately through
[GitHub security advisories](https://github.com/proseria-research/lineage/security/advisories/new),
not in a public issue.

## License

Lineage is licensed under the [Apache License 2.0](LICENSE). Under section 5 of that license,
anything you submit for inclusion is licensed on the same terms.
