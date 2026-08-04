#!/bin/sh
# One-shot fleet-db auth seeder, run as a compose service (redis:7-alpine)
# AFTER redis is healthy and BEFORE fleet-db starts taking real traffic.
#
# fleet-db keeps API keys and ACL roles in Redis; with real auth enabled
# (FLEET_AUTH_DEV_MODE=false) the only bootstrap path that needs no existing
# admin credential is writing the well-known keys directly:
#
#   fleet-db:auth:apikey:<key>          -> JSON auth.Actor  (X-API-Key auth)
#   fleet-db:acl:global-roles:<actorID> -> role string      (authz)
#
# Key formats per fleet-db/internal/storage/keys.go (AuthAPIKeyKey,
# ACLGlobalRoleKey) and fleet-db/internal/auth/redis_keystore.go (the stored
# value is the JSON-encoded Actor; the struct has no json tags, so field
# names are the exported Go names).
#
# The seeded actor gets a GLOBAL role (default: admin) so loom serve's
# background sweepers — including the await-timeout sweeper, which needs
# admin/maintainer — pass authz in every workspace.
#
# The API key value is taken from the environment and never echoed.
set -eu

: "${LOOM_FLEET_DB_API_KEY:?LOOM_FLEET_DB_API_KEY must be set (generated into .env)}"
ACTOR="${FLEET_SEED_ACTOR:?FLEET_SEED_ACTOR must be set}"
ROLE="${FLEET_SEED_ROLE:-admin}"
REDIS_HOST="${FLEET_SEED_REDIS_HOST:-redis}"
REDIS_PORT="${FLEET_SEED_REDIS_PORT:-6379}"

case "$ROLE" in
  admin|maintainer|developer|viewer) ;;
  *) echo "FLEET_SEED_ROLE must be one of admin|maintainer|developer|viewer" >&2; exit 2 ;;
esac

actor_json="{\"ID\":\"${ACTOR}\",\"DisplayName\":\"podman-stack control plane\",\"AuthMethod\":\"api_key\"}"

# Values pass as argv inside this short-lived seed container only; stdout of
# the key-bearing command is discarded so the key never lands in logs.
redis-cli -h "$REDIS_HOST" -p "$REDIS_PORT" \
  SET "fleet-db:auth:apikey:${LOOM_FLEET_DB_API_KEY}" "$actor_json" >/dev/null
redis-cli -h "$REDIS_HOST" -p "$REDIS_PORT" \
  SET "fleet-db:acl:global-roles:${ACTOR}" "$ROLE" >/dev/null

echo "fleet auth seeded: actor=${ACTOR} global-role=${ROLE} (api key redacted)"
