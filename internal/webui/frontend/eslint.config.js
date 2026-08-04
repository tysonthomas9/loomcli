/**
 * ESLint configuration for the loomcli frontend.
 *
 * =============================================================================
 * FRONTEND LAYER DAG (authoritative — enforced via eslint-plugin-boundaries)
 * =============================================================================
 *
 * The frontend is organized into architectural layers. Runtime imports must
 * respect the DAG below. Type-only imports (`import type { ... }`) are allowed
 * across any edge so shared DTOs don't force runtime coupling.
 *
 * | Layer      | Pattern                      | Allowed runtime imports from           |
 * |------------|------------------------------|----------------------------------------|
 * | app        | src/*.{ts,tsx}               | app (intra), views, components,        |
 * |            | (App, main, router, fixtures)|  features, contexts, hooks, stores,    |
 * |            |                              |  styles, types                         |
 * | features   | src/features/<feature>/**    | same feature, shared components, api,  |
 * |            |                              |  utils, styles, and types              |
 * | views      | src/views/**                 | views (intra), components, contexts,   |
 * |            |                              |  hooks, stores, api, utils, styles,    |
 * |            |                              |  types                                 |
 * | contexts   | src/contexts/**              | hooks, stores, api, utils, types       |
 * | components | src/components/**            | components, contexts, hooks, utils,    |
 * |            |                              |  types (NO api, NO stores, NO views)   |
 * | hooks      | src/hooks/**                 | hooks, contexts, stores, api, utils,   |
 * |            |                              |  types (NO components, NO views)       |
 * | stores     | src/stores/**                | api, utils, types                      |
 * | api        | src/api/**                   | utils, types                           |
 * | utils      | src/utils/**                 | types                                  |
 * | styles     | src/styles/**                | types                                  |
 * | types      | src/types/**                 | (nothing — leaf)                       |
 *
 * Rules:
 *   1. default = "disallow". Any edge not explicitly listed is forbidden.
 *   2. Type-only imports (kind: "type") are allowed across every edge.
 *      Rationale: the goal of the DAG is to stop components from accumulating
 *      runtime data-fetching logic. `import type` is erased at compile time
 *      and introduces no runtime coupling.
 *   3. `@typescript-eslint/consistent-type-imports` is an error so the
 *      type-only escape hatch can actually be trusted — developers cannot
 *      write a value import that TS happens to erase.
 *   4. No allowlist. Every violation is either fixed in-repo or is a legal
 *      type-only import.
 *   5. Tests are exempt via `boundaries/ignore`. Production code only.
 *   6. Adding a new top-level directory under src/ requires adding it as a
 *      layer in this DAG — there is no "miscellaneous" layer.
 *
 * =============================================================================
 */

import js from "@eslint/js";
import tseslint from "typescript-eslint";
import react from "eslint-plugin-react";
import reactHooks from "eslint-plugin-react-hooks";
import importX from "eslint-plugin-import-x";
import boundaries from "eslint-plugin-boundaries";
import prettier from "eslint-config-prettier";

// Every layer in the DAG, for the type-only escape hatch.
const ALL_LAYERS = [
  "app",
  "features",
  "views",
  "contexts",
  "components",
  "hooks",
  "stores",
  "api",
  "utils",
  "styles",
  "types",
];

export default tseslint.config(
  js.configs.recommended,
  ...tseslint.configs.recommended,
  {
    files: ["src/**/*.{ts,tsx}"],
    plugins: {
      react,
      "react-hooks": reactHooks,
      "import-x": importX,
      boundaries,
    },
    languageOptions: {
      ecmaVersion: 2020,
      sourceType: "module",
      parserOptions: {
        ecmaFeatures: {
          jsx: true,
        },
      },
    },
    settings: {
      react: {
        version: "detect",
      },
      "import/resolver": {
        typescript: { project: "./tsconfig.json" },
      },
      "boundaries/elements": [
        { type: "app", pattern: "src/*.{ts,tsx}", mode: "file" },
        {
          type: "features",
          pattern: "src/features/(*)/**/*.{ts,tsx}",
          capture: ["feature"],
          mode: "full",
        },
        { type: "views", pattern: "src/views/**/*.{ts,tsx}", mode: "file" },
        {
          type: "contexts",
          pattern: "src/contexts/**/*.{ts,tsx}",
          mode: "file",
        },
        {
          type: "components",
          pattern: "src/components/**/*.{ts,tsx}",
          mode: "file",
        },
        { type: "hooks", pattern: "src/hooks/**/*.{ts,tsx}", mode: "file" },
        { type: "stores", pattern: "src/stores/**/*.{ts,tsx}", mode: "file" },
        { type: "api", pattern: "src/api/**/*.{ts,tsx}", mode: "file" },
        { type: "utils", pattern: "src/utils/**/*.{ts,tsx}", mode: "file" },
        { type: "styles", pattern: "src/styles/**/*.{ts,tsx}", mode: "file" },
        { type: "types", pattern: "src/types/**/*.{ts,tsx}", mode: "file" },
      ],
      "boundaries/ignore": [
        "src/test-utils/**",
        "src/**/__tests__/**",
        "src/**/*.test.{ts,tsx}",
        "src/**/*.spec.{ts,tsx}",
        "src/vite-env.d.ts",
      ],
    },
    rules: {
      // React rules
      ...react.configs.recommended.rules,
      "react/react-in-jsx-scope": "off", // Not needed with React 17+ JSX transform
      "react/prop-types": "off", // Using TypeScript for type checking

      // React Hooks rules
      "react-hooks/rules-of-hooks": "error",
      "react-hooks/exhaustive-deps": "warn",

      // TypeScript rules
      "@typescript-eslint/no-unused-vars": [
        "error",
        { argsIgnorePattern: "^_", varsIgnorePattern: "^_" },
      ],
      "@typescript-eslint/no-explicit-any": "warn",

      // Type-import discipline — load-bearing for the boundaries type-only
      // escape hatch. Without this, a developer could write
      // `import { Foo } from "@/api/foo"` that TS erases at runtime, bypassing
      // the value-import ban.
      //
      // disallowTypeAnnotations is off: `typeof import("x")` style annotations
      // are still valid for circular type refs (e.g., function return types
      // that would otherwise create an import cycle).
      "@typescript-eslint/consistent-type-imports": [
        "error",
        {
          prefer: "type-imports",
          fixStyle: "separate-type-imports",
          disallowTypeAnnotations: false,
        },
      ],

      // Import rules
      "import-x/no-unused-modules": [
        "warn",
        {
          unusedExports: true,
          missingExports: false,
          ignoreExports: [
            "src/components/index.ts",
            "src/components/*/index.ts",
            "src/components/**/index.ts",
            "src/hooks/index.ts",
            "src/hooks/*/index.ts",
            "src/api/index.ts",
            "src/api/*/index.ts",
            "src/types/index.ts",
            "src/types/*/index.ts",
            "src/styles/index.ts",
          ],
        },
      ],

      // Boundaries: file classification gate — every src/**/*.{ts,tsx} file
      // must belong to a layer. Adding a new top-level dir fails lint until
      // a layer is added to `boundaries/elements` above.
      "boundaries/no-unknown-files": "error",

      // Boundaries: dependency DAG. Enforced at error severity with no
      // allowlist. Every edge not listed in `rules` below is forbidden
      // (default: disallow). Type-only imports are allowed across every
      // edge via a separate escape-hatch rule.
      "boundaries/dependencies": [
        "error",
        {
          default: "disallow",
          rules: [
            // Runtime DAG edges (value imports only match; type-only imports
            // still hit the escape hatch below).
            {
              from: [{ type: "app" }],
              allow: [
                {
                  to: [
                    {
                      type: [
                        "app",
                        "features",
                        "views",
                        "components",
                        "contexts",
                        "hooks",
                        "stores",
                        "api",
                        "utils",
                        "styles",
                        "types",
                      ],
                    },
                  ],
                },
              ],
            },
            {
              from: [{ type: "features" }],
              allow: [
                {
                  to: [
                    {
                      type: "features",
                      captured: { feature: "{{from.feature}}" },
                    },
                    {
                      type: ["components", "api", "utils", "styles", "types"],
                    },
                  ],
                },
              ],
            },
            {
              from: [{ type: "views" }],
              allow: [
                {
                  to: [
                    {
                      type: [
                        "views",
                        "components",
                        "contexts",
                        "hooks",
                        "stores",
                        "api",
                        "utils",
                        "styles",
                        "types",
                      ],
                    },
                  ],
                },
              ],
            },
            {
              from: [{ type: "contexts" }],
              allow: [
                {
                  to: [
                    {
                      type: ["hooks", "stores", "api", "utils", "types"],
                    },
                  ],
                },
              ],
            },
            {
              from: [{ type: "components" }],
              allow: [
                {
                  to: [
                    {
                      type: [
                        "components",
                        "contexts",
                        "hooks",
                        "utils",
                        "styles",
                        "types",
                      ],
                    },
                  ],
                },
              ],
            },
            {
              from: [{ type: "hooks" }],
              allow: [
                {
                  to: [
                    {
                      type: [
                        "hooks",
                        "contexts",
                        "stores",
                        "api",
                        "utils",
                        "types",
                      ],
                    },
                  ],
                },
              ],
            },
            {
              from: [{ type: "stores" }],
              allow: [
                {
                  to: [
                    {
                      type: ["stores", "api", "utils", "types"],
                    },
                  ],
                },
              ],
            },
            {
              from: [{ type: "api" }],
              allow: [
                {
                  to: [
                    {
                      type: ["api", "utils", "types"],
                    },
                  ],
                },
              ],
            },
            {
              from: [{ type: "utils" }],
              allow: [
                {
                  to: [
                    {
                      type: ["utils", "types"],
                    },
                  ],
                },
              ],
            },
            {
              from: [{ type: "styles" }],
              allow: [
                {
                  to: [
                    {
                      type: ["styles", "types"],
                    },
                  ],
                },
              ],
            },
            {
              from: [{ type: "types" }],
              allow: [
                {
                  to: [
                    {
                      type: ["types"],
                    },
                  ],
                },
              ],
            },

            // Type-only escape hatch: any layer may import types from any
            // layer. Runtime-erased; no coupling introduced at runtime.
            {
              from: [{ type: ALL_LAYERS }],
              allow: [
                {
                  to: [{ type: ALL_LAYERS }],
                  dependency: { kind: "type" },
                },
              ],
            },
          ],
        },
      ],

      // A feature is consumed only through src/features/<feature>/index.ts.
      // Internal feature imports use relative paths and are separately bounded
      // to the same captured feature name above.
      "no-restricted-imports": [
        "error",
        {
          patterns: [
            {
              group: ["@/features/*/*"],
              message:
                "Import a frontend feature through its public @/features/<feature> entry.",
            },
          ],
        },
      ],
    },
  },
  {
    ignores: [
      "dist/**",
      "node_modules/**",
      "coverage/**",
      "*.config.*",
      "playwright-report/**",
      "test-results/**",
    ],
  },
  prettier,
);
