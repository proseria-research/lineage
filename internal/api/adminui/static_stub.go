//go:build !console

package adminui

import "net/http"

// spaHandler is the default when the console is NOT embedded (a plain `go build`, without the
// `console` tag — so `go build`/`go test ./...` compile with no committed or built assets).
// It serves a short placeholder; build with `-tags console` after `make web` — or use
// `make build` / the Docker image — to serve the real UI. The Model API (:8081) is unaffected.
func spaHandler() http.Handler {
	const page = `<!doctype html><html lang="en"><head><meta charset="utf-8">` +
		`<title>Lineage</title></head>` +
		`<body style="font-family:ui-monospace,SFMono-Regular,Menlo,monospace;color:#111;padding:2.5rem;line-height:1.6">` +
		`<p><span style="display:inline-block;width:11px;height:11px;border:1.5px solid #111;vertical-align:middle;margin-right:8px"></span>` +
		`<strong>Lineage</strong> — the admin console is not embedded in this build.</p>` +
		`<p>Build it with <code>make web &amp;&amp; go build -tags console ./cmd/lineage</code>, ` +
		`or use <code>make build</code> / the container image.</p>` +
		`</body></html>`
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(page))
	})
}
