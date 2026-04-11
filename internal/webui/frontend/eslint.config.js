import js from "@eslint/js";
import tseslint from "typescript-eslint";
import react from "eslint-plugin-react";
import reactHooks from "eslint-plugin-react-hooks";
import importX from "eslint-plugin-import-x";
import prettier from "eslint-config-prettier";

export default tseslint.config(
  js.configs.recommended,
  ...tseslint.configs.recommended,
  {
    files: ["src/**/*.{ts,tsx}"],
    plugins: {
      react,
      "react-hooks": reactHooks,
      "import-x": importX,
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
            "src/types/index.ts",
            "src/styles/index.ts",
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
  prettier
);
