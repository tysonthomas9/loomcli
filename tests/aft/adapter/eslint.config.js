import tseslint from 'typescript-eslint';
export default tseslint.config(
  { ignores: ['**/dist/**', '**/node_modules/**', '.adapter-dev/**'] },
  ...tseslint.configs.recommended,
);
