import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

// The SPA builds into ui/dist, which the BFF serves. Assets are never
// inlined as data: URIs, because the BFF's CSP has no data: source
// (DESIGN-0004 § The UI); scripts/check-dist.ts fails the build if one
// slips through. In development the BFF runs beside Vite (`bun run
// dev:server`, port 3000) and Vite proxies its paths to it.
export default defineConfig({
  root: "web",
  plugins: [react(), tailwindcss()],
  // The design system ships TypeScript source, so the dev server serves it
  // and what it imports file by file instead of pre-bundling them. That
  // works for ES modules and breaks for the CommonJS packages underneath
  // (a blank page: "does not provide an export named ..."). Naming them
  // here pre-bundles them. The production build is not affected.
  optimizeDeps: {
    include: [
      "@tanstack/react-table",
      "cmdk",
      "recharts",
      "sonner",
      "use-sync-external-store/shim",
      "use-sync-external-store/shim/with-selector",
      "use-sync-external-store/with-selector",
    ],
  },
  build: {
    outDir: "../dist",
    emptyOutDir: true,
    assetsDir: "assets",
    assetsInlineLimit: 0,
  },
  server: {
    proxy: {
      "/api": "http://localhost:3000",
      "/auth": "http://localhost:3000",
      "/ui": "http://localhost:3000",
    },
  },
});
