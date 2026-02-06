import js from "@eslint/js";
import tseslint from "typescript-eslint";
import react from "eslint-plugin-react";
import reactHooks from "eslint-plugin-react-hooks";
import importPlugin from "eslint-plugin-import-x";
import jsxA11y from "eslint-plugin-jsx-a11y";
import prettier from "eslint-config-prettier";

export default tseslint.config(
  js.configs.recommended,
  ...tseslint.configs.strict,
  {
    files: ["src/**/*.{ts,tsx}"],
    plugins: {
      react,
      "react-hooks": reactHooks,
      "import-x": importPlugin,
      "jsx-a11y": jsxA11y,
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
      "import-x/resolver": {
        typescript: {
          alwaysTryTypes: true,
          project: "./tsconfig.json",
        },
      },
    },
    rules: {
      // React rules
      ...react.configs.recommended.rules,

      // Accessibility rules
      ...jsxA11y.flatConfigs.recommended.rules,
      // Custom dropdowns/menus use div elements with click handlers - proper keyboard
      // support would require significant refactoring. Set to warn for tracking.
      "jsx-a11y/click-events-have-key-events": "warn",
      "jsx-a11y/no-static-element-interactions": "warn",
      "jsx-a11y/no-noninteractive-element-interactions": "warn",
      // autoFocus is used intentionally for better UX in search inputs and modal forms
      "jsx-a11y/no-autofocus": "off",
      // Custom select/tablist components need tabIndex refactoring
      "jsx-a11y/interactive-supports-focus": "warn",
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
      // Allow dynamic delete for exactOptionalPropertyTypes compliance in useFilterState
      "@typescript-eslint/no-dynamic-delete": "off",

      // Re-enable no-dupe-keys: typescript-eslint disables it assuming TS catches duplicates,
      // but TS doesn't flag dupes in unconstrained objects (e.g., vi.mock() factories)
      "no-dupe-keys": "error",

      // Import ordering and organization
      "import-x/order": [
        "error",
        {
          groups: [
            "builtin",
            "external",
            "internal",
            ["parent", "sibling", "index"],
          ],
          pathGroups: [
            {
              pattern: "@/**",
              group: "internal",
              position: "before",
            },
          ],
          pathGroupsExcludedImportTypes: ["builtin"],
          "newlines-between": "always",
          alphabetize: { order: "asc", caseInsensitive: true },
        },
      ],
      "import-x/no-cycle": "warn",
      "import-x/no-duplicates": "error",
      "import-x/first": "error",
    },
  },
  // Test files: allow non-null assertions since they're common/acceptable
  // when elements are known to exist after queries
  {
    files: ["src/**/__tests__/**/*.{ts,tsx}", "src/**/*.test.{ts,tsx}"],
    rules: {
      "@typescript-eslint/no-non-null-assertion": "off",
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
  prettier
);
