#!/usr/bin/env bash
set -euo pipefail

# Hide cursor so screenshot comparison is stable.
printf '\033[?25l'

# Clear screen + move cursor home.
printf '\033[2J\033[H'

printf 'DETERMINISTIC TERMINAL PARITY\n'
printf 'FRAME: STATIC-001\n'
printf '%s\n' '----------------------------------------'
printf '\033[32mOK\033[0m  \033[33mWARN\033[0m  \033[31mERR\033[0m\n'
printf 'alpha line\n'
printf 'beta line\n'
printf 'gamma line\n'
printf '\033[7mSTATUS: READY\033[0m\n'

# Keep session alive without repainting, so screenshot parity is deterministic.
while true; do
  sleep 3600 || true
done
