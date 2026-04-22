# Parity Run Toolchain — 2026-04-22

Record of the environment used to produce the snapshotted parity artifacts.

| Component | Version / SHA | Notes |
|---|---|---|
| Go | 1.25.6 linux/arm64 | loomcli build env |
| bd | 0.49.0 (dev) | built from `loomcli/third_party/beads/cmd/bd` via `make install-bd`; binary at `/home/admin/go/bin/bd` |
| fleet-db | `b571c75` (local checkout at `~/codebase/fleet-db/`) | clean checkout, no local modifications beyond artifacts/ |
| Redis | in-process `miniredis/v2` | fleet-db parity harness embeds; no external redis-server used |
| Harness invocation | `cd ~/codebase/fleet-db && PARITY_DUAL_RUN=1 make test-parity` | `PARITY_DUAL_RUN=1` is required to activate dual-backend comparison; without it the harness runs in `fleet_db_only` mode |
| Loomcli branch | `beads-vs-fleet-parity` off `v4` (base: 40a902cf) | |

## Reproducing

```bash
# 1. Ensure bd is installed
cd /home/admin/codebase/2/loomcli
make install-bd
which bd && bd --version   # expect 0.49.0 (dev)

# 2. Run the harness
cd ~/codebase/fleet-db
PARITY_DUAL_RUN=1 make test-parity

# 3. Artifacts land at
ls ~/codebase/fleet-db/artifacts/parity/
#   diff-report.json
#   release-report.md
```

Exit criteria met this run:
- `mode: dual_run`
- `beads_available: true`
- `total_comparisons: 619` (non-zero)
