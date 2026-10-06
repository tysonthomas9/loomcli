#!/bin/sh
set -eu

remote="$1"
shift
case "$*" in
    *"git-upload-pack 'owner/"*".git'"*) exec git-upload-pack "$remote" ;;
    *"git-receive-pack 'owner/"*".git'"*) exec git-receive-pack "$remote" ;;
    *) echo "unexpected fake Git transport request" >&2; exit 1 ;;
esac
