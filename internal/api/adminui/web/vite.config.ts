import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import path from "node:path";

// The console is served from the :8080 BFF root; assets live under /assets (default base).
// Output (dist) is embedded into the binary only under the `console` build tag, and is not
// committed — see internal/api/adminui/static_*.go.
export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: { alias: { "@": path.resolve(import.meta.dirname, "src") } },
  build: { outDir: "dist", emptyOutDir: true },
  server: { proxy: { "/api": "http://localhost:8080" } },
});
