# Third-party notices

Loom includes code adapted from the projects below. Each ported file names
its source in a header comment.

## T3 Code

Source: https://github.com/pingdotgg/t3code at commit `2daff8c25`.

Ported files (internal/webui/frontend/src/components/AgentChat/):

- `ChatMarkdown.tsx`, `ChatMarkdown.module.css`: from `apps/web/src/components/ChatMarkdown.tsx`, `apps/web/src/markdown-clipboard.ts` and `apps/web/src/index.css`.
- `MessageCopyButton.tsx`: from `apps/web/src/components/chat/MessageCopyButton.tsx`.
- `timelineRows.ts`: from `apps/web/src/components/chat/MessagesTimeline.logic.ts`.
- `WorkRows.tsx`, `Timeline.module.css`: from `apps/web/src/components/chat/MessagesTimeline.tsx` and `apps/web/src/index.css`.
- `pendingUserInput.ts`, `__tests__/pendingUserInput.test.ts`: from `apps/web/src/pendingUserInput.ts` and its test.
- `ComposerPendingApprovalPanel.tsx`, `ComposerPendingApprovalActions.tsx`, `ComposerPendingUserInputPanel.tsx`, `ThreadErrorBanner.tsx`: from the files of the same name in `apps/web/src/components/chat/`.
- `PendingAsk.module.css`: from those components' Tailwind classes and `apps/web/src/components/chat/ComposerPrimaryActions.tsx`.
- `AskCard.tsx`: the pending question's primary action label from `apps/web/src/components/chat/ComposerPrimaryActions.tsx`.

Ported files for the composer model, provider and effort pickers (UI2; internal/webui/frontend/src/components/AgentChat/):

- `ProviderModelPicker.tsx`: from `apps/web/src/components/chat/ProviderModelPicker.tsx`.
- `ModelPickerContent.tsx`: from `apps/web/src/components/chat/ModelPickerContent.tsx` and `apps/web/src/components/chat/ModelListRow.tsx`.
- `ModelPickerSidebar.tsx`: from `apps/web/src/components/chat/ModelPickerSidebar.tsx`.
- `TraitsPicker.tsx`: from `apps/web/src/components/chat/TraitsPicker.tsx`.
- `ComposerControl.tsx`: from `apps/web/src/components/chat/ComposerControl.tsx`.
- `CompactComposerControlsMenu.tsx`: from `apps/web/src/components/chat/CompactComposerControlsMenu.tsx` (without its plan/build mode and access groups).
- `ProviderIcon.tsx`: from `apps/web/src/components/chat/ProviderInstanceIcon.tsx`, `apps/web/src/components/chat/providerIconUtils.ts` and the OpenAI, ClaudeAI and OpenCode glyphs in `apps/web/src/components/Icons.tsx`.
- `modelPickerSearch.ts`: from `apps/web/src/components/chat/modelPickerSearch.ts` and `packages/shared/src/searchRanking.ts`.
- `ModelPicker.module.css`: from the Tailwind classes of the files above.

Ported files for the chat page design (UI0; internal/webui/frontend/src/components/AgentChat/):

- `ChatHeader.tsx`: from `apps/web/src/components/chat/ChatHeader.tsx` (the title, its inline rename and `resolveRenameCommit`; without the project breadcrumb, thread menu, project scripts, open-in and git actions).
- `ChatComposer.tsx`: from `apps/web/src/components/chat/ChatComposer.tsx`, `apps/web/src/components/chat/ComposerPrimaryActions.tsx` and `apps/web/src/components/ComposerPromptEditor.tsx`.
- `MessageRows.tsx`: from `UserTimelineRow`, `CollapsibleUserMessageBody`, `WorkingTimelineRow`, `WorkingTimer` and `formatWorkingTimer` in `apps/web/src/components/chat/MessagesTimeline.tsx`.
- `ChatPage.module.css`: from `apps/web/src/index.css` (the composer glass shell and host, the theme's radius, message and message-action tokens) and the Tailwind classes of `apps/web/src/components/ChatView.tsx` and the files above.
- `AgentChat.tsx` (the page layout, message column and scroll-to-end): from `apps/web/src/components/ChatView.tsx` and `apps/web/src/components/chat/MessagesTimeline.tsx`.

Ported motion for the chat (UI5; internal/webui/frontend/src/components/AgentChat/):

- `ChatPage.module.css`: the pending-ask drawer's entrance, from `apps/web/src/components/chat/ComposerBannerStack.tsx` (opacity and a 4px rise only, without its height transition).
- `MessageRows.tsx`: the working row's step label, from `WorkingTimelineRow` in `apps/web/src/components/chat/MessagesTimeline.tsx`, and its live-activity shimmer redone as an opacity pulse.
- `ModelPickerSidebar.tsx`, `ModelPickerContent.tsx`, `ModelPicker.module.css`: the sliding selected-provider indicator and the model list's scroll fades, from `apps/web/src/components/chat/ModelPickerSidebar.tsx`, `apps/web/src/components/chat/ModelPickerContent.tsx` and `apps/web/src/components/ui/scroll-area.tsx`.

Ported files (internal/loomharness/):

- `codex/catalog.go`: from `apps/server/src/provider/Layers/CodexProvider.ts`.
- `opencode/catalog.go`: from `apps/server/src/provider/Layers/OpenCodeProvider.ts`.
- `claude/catalog.go`: from `apps/server/src/provider/Layers/ClaudeProvider.ts`.
- `claude/events.go` (`resultError`): from `resultUserFacingError` in `apps/server/src/provider/Layers/ClaudeAdapter.ts`.

```
MIT License

Copyright (c) 2026 T3 Tools Inc.

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```
