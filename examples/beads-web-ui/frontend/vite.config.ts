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
          xterm: [
            "@xterm/xterm",
            "@xterm/addon-fit",
            "@xterm/addon-web-links",
          ],
        },
      },
    },
  },

  server: {
    port: 3000,
    // Fail fast if port is in use (ensures proxy aligns with Go backend)
    strictPort: true,
    // Proxy API calls to Go backend during development
    proxy: {
      "/api": {
        target: "http://localhost:8080",
        changeOrigin: true,
        ws: true,
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
  },
}))
