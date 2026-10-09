import { z } from 'zod';
import { Id, Digest, RelativePath } from './protocol.js';

export const RENDERER_SOURCE_PATHS=Object.freeze({
  chatMarkdownSha256:'src/components/AgentChat/ChatMarkdown.tsx',longTextSha256:'src/components/AgentChat/LongText.tsx',
  codeHighlightSha256:'src/components/AgentChat/codeHighlight.ts',messageCopySha256:'src/components/AgentChat/MessageCopyButton.tsx',
  cssSha256:'src/components/AgentChat/ChatMarkdown.module.css',lockSha256:'package-lock.json',
} as const);
export const RENDERER_DEPENDENCIES=Object.freeze(['react','react-dom','react-markdown','remark-gfm','rehype-sanitize','esbuild','jsdom'] as const);
export const RENDERER_SOURCE_FILES=Object.freeze(Object.values(RENDERER_SOURCE_PATHS));
export const RENDERER_PACKAGE_FILES=Object.freeze(RENDERER_DEPENDENCIES.map(name=>`${name}/package.json`));
export const RendererFileSchema=z.object({relativePath:RelativePath,sha256:Digest}).strict();
const Closure={sources:z.array(RendererFileSchema).length(RENDERER_SOURCE_FILES.length),
  packages:z.array(RendererFileSchema).length(RENDERER_PACKAGE_FILES.length),build:z.array(RendererFileSchema).min(1).max(50000)};
/** Post-build receipt is sealed into the enclosing source/build manifest before acquisition. */
export const RendererSourceBuildReceipt=z.object({version:z.literal(1),sourceManifestSha256:Digest,
  installedRelativeRoot:RelativePath,buildRelativeRoot:RelativePath,...Closure}).strict();
export const RendererTargetIdentity=z.object({targetId:Id,generation:Id,receiptSha256:Digest,
  sourceRootId:Id,installedRootId:Id,buildRootId:Id}).strict();
export const RendererPhysicalRootSchema=z.object({path:Id,device:z.number().int().nonnegative(),inode:z.number().int().nonnegative()}).strict();
/** Acquisition binds measured build bytes to the exact owning fixture target/generation. */
export const RendererBuildReceipt=z.object({version:z.literal(1),fixtureLeaseId:Id,targetId:Id,generation:Id,
  sourceRootId:Id,installedRootId:Id,buildRootId:Id,
  roots:z.object({source:RendererPhysicalRootSchema,installed:RendererPhysicalRootSchema,build:RendererPhysicalRootSchema}).strict(),...Closure}).strict();
