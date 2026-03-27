# ── Stage 1: Build ──
# node:22-alpine as of 2026-03-27
FROM node:22-alpine@sha256:4d64b49e6c891c8fc821007cb1cdc6c0db7773110ac2c34bf2e6960adef62ed3 AS builder

RUN apk add --no-cache python3 make g++

WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci --legacy-peer-deps
COPY tsconfig.json ./
COPY src/ ./src/
RUN npm run build
RUN npm prune --production

# ── Stage 2: Runtime ──
# node:22-alpine as of 2026-03-27
FROM node:22-alpine@sha256:4d64b49e6c891c8fc821007cb1cdc6c0db7773110ac2c34bf2e6960adef62ed3

RUN apk add --no-cache curl
WORKDIR /app

COPY --from=builder /app/dist ./dist
COPY --from=builder /app/node_modules ./node_modules
COPY --from=builder /app/package.json ./
COPY drizzle/ ./drizzle/
COPY drizzle-sqlite/ ./drizzle-sqlite/

RUN mkdir -p /app/data
RUN addgroup -g 1001 -S authuser && \
    adduser -S authuser -u 1001 -G authuser && \
    chown -R authuser:authuser /app
USER authuser

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD curl -f http://localhost:3001/health || exit 1

EXPOSE 3001
CMD ["node", "dist/index.js"]
