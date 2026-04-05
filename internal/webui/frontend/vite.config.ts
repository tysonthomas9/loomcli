/// <reference types="vitest" />
import { defineConfig } from "vite"
import react from "@vitejs/plugin-react"
import path from "path"

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
    // Optimize for Go embedding
    assetsDir: "assets",
    // Generate source maps for production debugging
    sourcemap: mode === "development",
    // Rollup options for chunking
    rollupOptions: {
      output: {
        // Predictable asset names for Go embedding
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
            target: "http://localhost:8080",
            changeOrigin: true,
          },
          "/health": {
            target: "http://localhost:8080",
            changeOrigin: true,
          },
        },
  },

  preview: {
    port: 3000,
    strictPort: true,
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
