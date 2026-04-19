# Origin

This package is a direct port of `cmd/entire/cli/transcript/` and the
`StripIDEContextTags` helper from `cmd/entire/cli/textutil/` in the
[entireio/cli](https://github.com/entireio/cli) project.

## Upstream

- Repository: https://github.com/entireio/cli
- License: MIT (Copyright (c) 2026 Entire Inc.)
- Ported from commit available in `/home/admin/codebase/cli` at port time
  (2026-04-17).

## Scope of the port

- `types.go` — verbatim port of Line, AssistantMessage, ContentBlock,
  ToolInput, UserMessage, and the Type/ContentType constants.
- `parse.go` — verbatim port of ParseFromBytes, ParseFromFileAtLine,
  SliceFromLine, ExtractUserContent, normalizeLineType.
- `strip_tags.go` — inlined port of StripIDEContextTags (upstream lives in
  a separate `textutil` package). Inlined because it is the only dependency
  of ExtractUserContent and is small and self-contained.
- `parse_test.go` — verbatim port of the upstream tests.

## MIT License text

Reproduced in `LICENSE.upstream` per the MIT license's requirement that
the notice be included in copies or substantial portions of the Software.

## Local changes

- Package doc comment updated to reference loom's session capture path.
- Import path for StripIDEContextTags changed from
  `github.com/entireio/cli/cmd/entire/cli/textutil` to the local package.

If you update this package against newer upstream, please update this file
and `LICENSE.upstream` with the new source commit.
