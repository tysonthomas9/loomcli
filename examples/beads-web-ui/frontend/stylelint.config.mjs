/** @type {import('stylelint').Config} */
export default {
  extends: [
    'stylelint-config-standard',
    'stylelint-config-css-modules',
  ],
  rules: {
    // Allow vendor prefixes like -webkit-line-clamp (used in IssueCard.module.css)
    'property-no-vendor-prefix': null,
    'value-no-vendor-prefix': null,
    // Allow modern CSS functions that stylelint may not recognize yet
    'function-no-unknown': [true, {
      ignoreFunctions: ['color-mix'],
    }],
    // Allow camelCase class selectors (project convention for CSS Modules)
    'selector-class-pattern': null,
    // Allow legacy rgba()/hsla() notation (used throughout codebase)
    'color-function-notation': null,
    // Allow 0.1 alpha notation (used throughout codebase)
    'alpha-value-notation': null,
    // Allow string @import notation (used in index.css)
    'import-notation': null,
    // Allow uppercase font family names (more readable)
    'value-keyword-case': ['lower', {
      ignoreFunctions: ['local'],
      ignoreKeywords: [
        'Arial', 'Helvetica', 'SFMono-Regular', 'Menlo', 'Monaco',
        'Consolas', 'Liberation', 'Mono', 'Courier', 'New',
      ],
    }],
    // Allow long hex colors (more explicit)
    'color-hex-length': null,
    // Allow comment without preceding empty line (stylistic preference)
    'comment-empty-line-before': null,
    // Disable selector ordering rules (conflicts with BEM-style patterns)
    'no-descending-specificity': null,
    // Allow duplicate selectors (sometimes needed for responsive overrides)
    'no-duplicate-selectors': null,
    // Allow camelCase keyframe names (consistent with class naming convention)
    'keyframes-name-pattern': null,
    // Allow break-word value (widely supported despite deprecation warnings)
    'declaration-property-value-keyword-no-deprecated': null,
    // Allow empty blocks (used for :has() selector targets)
    'block-no-empty': null,
  },
};
