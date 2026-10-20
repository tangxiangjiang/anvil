// @ts-check
import eslint from '@eslint/js';
import globals from 'globals';
import tseslint from 'typescript-eslint';

export default tseslint.config(
  {
    // Vendored third-party code is shipped, not maintained: linting it would
    // mean either fixing a bundle nobody here wrote or silencing the same
    // findings on every upgrade. It is checked by its version and by the one
    // property this repository cares about, in `scripts/vendor-katex.mjs`.
    ignores: ['out/**', 'node_modules/**', 'media/vendor/**'],
  },
  eslint.configs.recommended,
  ...tseslint.configs.recommended,
  {
    rules: {
      // The host protocol is JSON, so `any` appears at the wire boundary; the
      // checked surface is protocol.ts, which the rest of the code goes through.
      '@typescript-eslint/no-explicit-any': 'off',
      '@typescript-eslint/no-unused-vars': ['error', { argsIgnorePattern: '^_' }],
      'no-console': 'error',
      eqeqeq: ['error', 'always'],
    },
  },
  {
    // The plain-JavaScript pieces — the build script and the test fixture host —
    // are Node programs, not browser code, and printing is how they talk to a
    // person.
    files: ['**/*.mjs'],
    languageOptions: {
      globals: globals.node,
    },
    rules: {
      'no-console': 'off',
    },
  },
  {
    // The webview script runs in a browser context with one injected global.
    files: ['media/**/*.js'],
    languageOptions: {
      globals: { ...globals.browser, acquireVsCodeApi: 'readonly' },
    },
  },
);
