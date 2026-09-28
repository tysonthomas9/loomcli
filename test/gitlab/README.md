# Loom Git test lab

`make git-lab-test` reuses a content-addressed Podman image and runs every YAML scenario
under Git 2.40.4 and Git 2.56.0. `TASK=P1.3 make git-lab-test` selects one
task; `SCENARIO='*capture.yaml' make git-lab-test` selects scenario filenames.
The runner rejects unknown YAML fields, unknown steps, and wrong value types.
Each failure prints the scenario file, phase, step number, and step text.

The `loomcli` fixture is a fresh Git repository populated from the pinned
`31bacba3333e461e55bfee4b5d691dc6df3669e6` source tree (more than 3,000
files). It adds tracked `.npmrc` and `src/id_utils.go`. The `mixed` fixture
adds `.env`, `.env.local`, `id_rsa`, a symlink, empty directory, nested Git
repository, submodule, LFS file, sparse 150 MiB file, and unreadable file.
`empty` contains one initial commit. The provider fixture is a local bare Git
remote with a pre-receive hook for 100 MiB per file, 2 GiB per push, and secret
paths. The lab also mounts a 1 MiB tmpfs, has a `labother` Unix user, and can
kill a child process with SIGKILL. No external provider is contacted.

The fixed step vocabulary is:

| Step | Example | Meaning |
|---|---|---|
| `edit` | `edit: {path: README.md, append: "extra"}` | Append to an existing file. |
| `write` | `write: {path: work.txt, content: "new"}` | Write a file. |
| `git` | `git: [status, --short]` | Run one real Git command in the fixture. |
| `branch` | `branch: loom` | Create a user branch. |
| `commit` | `commit: "edit"` | Commit tracked changes. |
| `mkdir` | `mkdir: empty-dir` | Create a directory. |
| `symlink` | `symlink: {path: link, target: README.md}` | Create a symlink. |
| `chmod` | `chmod: {path: work.txt, mode: 256}` | Set Unix mode (`256` is 0400). |
| `capture` | `capture: {attempt: A1}` | Call the production capture engine with default Git options. |
| `diff_vs_head` | `diff_vs_head: {changed: [work.txt]}` | Require exactly these changed paths in the capture. |
| `tree_has` | `tree_has: [README.md]` | Require paths in the captured tree. |
| `tree_lacks` | `tree_lacks: [.env]` | Require paths absent from the captured tree. |
| `manifest` | `manifest: {".env": secret_suspect}` | Check path classifications. |
| `capture_complete` | `capture_complete: false` | Check capture completeness. |
| `took_under` | `took_under: 10s` | Bound the last capture duration. |
| `go_check` | `go_check: journal_crash` | Run a named Go assertion for behavior too complex for YAML. |

Named Go checks are in `internal/loomgit/gitlab/checks_test.go`. Each has a
one-line comment describing its proof. Some checks invoke the existing real
Git package regressions; the scenario runner names the exact tests and runs
them under the selected Git binary with `CGO_ENABLED=0`.

The image runs only the Git lab and does not start Loom local-mode. Its
`loomgit-lab:<hash>` tag hashes the Containerfile, runner, and Go module inputs,
so unchanged runs reuse one image. A separate build stage keeps Git compilers
out of the test image. Each run uses a unique container name and
`podman run --rm`. The host script checks `podman ps` and refuses to build
under 10 GiB free. macOS APFS `clonefile` behavior cannot be tested in this
Linux container; CoW tests remain host-only. The image remains in the local
store for reuse; remove only images from your own lab runs when reclaiming
space.
