/// <reference types="vitest" />
import { defineConfig } from "vite"
import react from "@vitejs/plugin-react"
import path from "path"

// Dev proxy target: forward /api and /health requests to this URL.
// Defaults to the local Go server when VITE_API_BASE_URL is unset.
const apiProxyTarget = process.env.VITE_API_BASE_URL || "http://localhost:8080";

export default defineConfig(({ mode }) => ({
  plugins: [react()],

  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
    },
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
        // Manual chunks for better caching
        manualChunks: {
          react: ["react", "react-dom"],
          "react-flow": ["@xyflow/react"],
          "better-auth": ["better-auth/react", "better-auth/client/plugins"],
          codemirror: [
            "@codemirror/state",
            "@codemirror/view",
            "@codemirror/commands",
            "@codemirror/language",
            "@codemirror/search",
            "@codemirror/lang-go",
            "@codemirror/lang-json",
            "@codemirror/lang-yaml",
            "@codemirror/lang-markdown",
          ],
        },
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
            changeOrigin: true,
            ws: true,
          },
          "/health": {
            target: apiProxyTarget,
            changeOrigin: true,
          },
        },
  },

  preview: {
    port: 3000,
    strictPort: true,
    // Proxy API calls and WebSocket traffic to the Go server during e2e tests.
    // Disabled during Playwright unit tests so page.route() mocks can intercept requests.
    proxy: process.env.PLAYWRIGHT_TEST
      ? undefined
      : {
          "/api": {
            target: process.env.E2E_API_URL || "http://localhost:8080",
            changeOrigin: true,
            ws: true,
          },
          "/health": {
            target: process.env.E2E_API_URL || "http://localhost:8080",
            changeOrigin: true,
          },
        },
  },

  test: {
    globals: true,
    environment: "node",
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
}))
