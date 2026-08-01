# syntax=docker/dockerfile:1

# 1) Build the embedded Admin console (Vite/React). Its output is a directory of static
# assets, identical whatever the target architecture, so this stage is pinned to the
# builder's own platform and runs once for a multi-arch build.
FROM --platform=$BUILDPLATFORM node:22-alpine AS web
WORKDIR /web
RUN corepack enable
# package.json first, so corepack resolves the pnpm pinned in `packageManager` rather than
# whatever is newest — recent pnpm treats an unapproved build script as a hard error, and an
# unpinned toolchain turns that into a build that breaks on a day nothing here changed.
COPY internal/api/adminui/web/package.json internal/api/adminui/web/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY internal/api/adminui/web/ ./
RUN pnpm build

# 2) Build the single static Go binary (cgo-free: modernc sqlite + pgx), embedding the
# console. Also pinned to the build platform and cross-compiled via GOARCH: the binary has
# no cgo, so this is a native compile targeting another architecture rather than an emulated
# one, which is the difference between seconds and many minutes under QEMU.
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /web/dist ./internal/api/adminui/web/dist
ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -tags console -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/lineage ./cmd/lineage

# 3) Minimal, non-root runtime.
FROM gcr.io/distroless/static-debian12:nonroot
LABEL org.opencontainers.image.source="https://github.com/proseria-research/lineage"
COPY --from=build /out/lineage /usr/local/bin/lineage
USER nonroot:nonroot
EXPOSE 8080 8081 9090
ENTRYPOINT ["/usr/local/bin/lineage"]
