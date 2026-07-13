/// <reference types="vitest" />
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import path from "path";

// Dev proxy target: forward /api and /health requests to this URL.
// Defaults to the local Go server when VITE_API_BASE_URL is unset.
const apiProxyTarget = process.env.VITE_API_BASE_URL || "http://localhost:8080";

function includesAny(id: string, needles: string[]): boolean {
  return needles.some((needle) => id.includes(needle));
}

function manualChunks(id: string): string | undefined {
  const normalizedId = id.split(path.sep).join("/");
  if (!normalizedId.includes("/node_modules/")) return undefined;

  if (
    includesAny(normalizedId, [
      "/node_modules/react/",
      "/node_modules/react-dom/",
      "/node_modules/scheduler/",
    ])
  ) {
    return "react";
  }

  if (normalizedId.includes("/node_modules/react-router")) {
    return "react-router";
  }

  if (normalizedId.includes("/node_modules/@xyflow/")) {
    return "react-flow";
  }

  if (normalizedId.includes("/node_modules/better-auth/")) {
    return "better-auth";
  }

  if (normalizedId.includes("/node_modules/@wterm/")) {
    return "terminal-vendor";
  }

  if (normalizedId.includes("/node_modules/@dnd-kit/")) {
    return "dnd-kit";
  }

  if (normalizedId.includes("/node_modules/@tanstack/")) {
    return "virtual-list";
  }

  if (normalizedId.includes("/node_modules/@dagrejs/")) {
    return "graph-layout";
  }

  if (
    includesAny(normalizedId, [
      "/node_modules/@codemirror/state/",
      "/node_modules/@codemirror/view/",
      "/node_modules/crelt/",
      "/node_modules/style-mod/",
      "/node_modules/w3c-keyname/",
      "/node_modules/@marijn/",
    ])
  ) {
    return "codemirror-view";
  }

  // On-demand language packs (dynamically imported by CodeMirrorEditor) and
  // their unique grammars are kept OUT of the eager codemirror-language chunk so
  // they load only when a file of that type is opened. Shared lezer core
  // (@lezer/common, @lezer/lr, @lezer/highlight) stays eager below.
  if (
    includesAny(normalizedId, [
      "/node_modules/@codemirror/lang-python/",
      "/node_modules/@codemirror/lang-rust/",
      "/node_modules/@codemirror/lang-sql/",
      "/node_modules/@codemirror/lang-xml/",
      "/node_modules/@codemirror/lang-cpp/",
      "/node_modules/@codemirror/lang-php/",
      "/node_modules/@codemirror/legacy-modes/",
      "/node_modules/@lezer/python/",
      "/node_modules/@lezer/rust/",
      "/node_modules/@lezer/cpp/",
      "/node_modules/@lezer/php/",
      "/node_modules/@lezer/xml/",
    ])
  ) {
    return undefined;
  }

  if (
    includesAny(normalizedId, [
      "/node_modules/@codemirror/commands/",
      "/node_modules/@codemirror/language/",
      "/node_modules/@codemirror/search/",
      "/node_modules/@codemirror/autocomplete/",
      "/node_modules/@codemirror/lang-",
      "/node_modules/@lezer/",
      "/node_modules/codemirror-lang-diff/",
    ])
  ) {
    return "codemirror-language";
  }

  if (
    includesAny(normalizedId, [
      "/node_modules/react-markdown/",
      "/node_modules/remark-",
      "/node_modules/rehype-",
      "/node_modules/unified/",
      "/node_modules/mdast-util",
      "/node_modules/hast-util",
      "/node_modules/micromark",
      "/node_modules/unist-util",
      "/node_modules/vfile",
      "/node_modules/vfile-message",
      "/node_modules/property-information",
      "/node_modules/space-separated-tokens",
      "/node_modules/comma-separated-tokens",
      "/node_modules/decode-named-character-reference",
      "/node_modules/html-url-attributes",
      "/node_modules/trim-lines",
      "/node_modules/bail/",
      "/node_modules/devlop/",
      "/node_modules/zwitch/",
    ])
  ) {
    return "markdown";
  }

  if (normalizedId.includes("/node_modules/dompurify/")) {
    return "sanitize";
  }

  if (normalizedId.includes("/node_modules/openapi-fetch/")) {
    return "openapi";
  }

  if (
    includesAny(normalizedId, [
      "/node_modules/zustand/",
      "/node_modules/immer/",
    ])
  ) {
    return "state";
  }

  return "vendor";
}

export default defineConfig(({ mode }) => ({
  plugins: [react()],

  resolve: {
    alias: [
      {
        find: /^react-arborist$/,
        replacement: "react-arborist/dist/module/index.js",
      },
      { find: "@", replacement: path.resolve(__dirname, "./src") },
    ],
  },

  build: {
    outDir: "dist",
    emptyOutDir: true,
    // Static asset organization
    assetsDir: "assets",
    // Generate source maps for production debugging
    sourcemap: mode === "development",
    // Rollup options for chunking
    rollupOptions: {
      output: {
        // Predictable asset names for cache busting
        entryFileNames: "assets/[name]-[hash].js",
        chunkFileNames: "assets/[name]-[hash].js",
        assetFileNames: "assets/[name]-[hash][extname]",
        // Keep heavy shared dependencies out of the shell entry chunk.
        manualChunks,
      },
    },
  },

  server: {
    port: 3000,
    // Fail fast if port is in use (ensures proxy aligns with Go backend)
    strictPort: true,
    // Proxy API calls to Go backend during development.
    // Disabled during Playwright tests so page.route() mocks can intercept requests.
    proxy: process.env.PLAYWRIGHT_TEST
      ? undefined
      : {
          "/api": {
            target: apiProxyTarget,
            // Preserve the browser-facing Host. Local file access authorizes
            // same-origin GETs by matching it to the configured frontend URL.
            changeOrigin: false,
            ws: true,
          },
          "/health": {
            target: apiProxyTarget,
            changeOrigin: false,
          },
        },
  },

  preview: {
    port: 3000,
    strictPort: true,
    // Vite preview is used only for real integration tests, where the built
    // frontend must talk to the Go server. Keep this proxy enabled even when
    // Playwright is the parent process.
    proxy: {
      "/api": {
        target: process.env.E2E_API_URL || "http://localhost:8080",
        changeOrigin: false,
        ws: true,
      },
      "/health": {
        target: process.env.E2E_API_URL || "http://localhost:8080",
        changeOrigin: false,
      },
    },
  },

  test: {
    globals: true,
    environment: "node",
    setupFiles: ["src/test-utils/setup.ts"],
    exclude: ["tests/e2e/**", "node_modules/**"],
    pool: "forks",
    coverage: {
      provider: "v8",
      include: ["src/**/*.{ts,tsx}"],
      exclude: [
        "src/**/*.test.{ts,tsx}",
        "src/**/__tests__/**",
        "src/test-utils/**",
        "src/vite-env.d.ts",
        "src/main.tsx",
      ],
      thresholds: {
        lines: 60,
        branches: 60,
        functions: 60,
        statements: 60,
      },
    },
  },
}));
