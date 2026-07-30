# syntax=docker/dockerfile:1

# 1) Build the embedded Admin console (Vite/React).
FROM node:22-alpine AS web
WORKDIR /web
RUN corepack enable
COPY internal/api/adminui/web/package.json internal/api/adminui/web/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY internal/api/adminui/web/ ./
RUN pnpm build

# 2) Build the single static Go binary (cgo-free: modernc sqlite + pgx), embedding the console.
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /web/dist ./internal/api/adminui/web/dist
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/lineage ./cmd/lineage

# 3) Minimal, non-root runtime.
FROM gcr.io/distroless/static-debian12:nonroot
LABEL org.opencontainers.image.source="https://github.com/proseria-research/lineage"
COPY --from=build /out/lineage /usr/local/bin/lineage
USER nonroot:nonroot
EXPOSE 8080 8081 9090
ENTRYPOINT ["/usr/local/bin/lineage"]
