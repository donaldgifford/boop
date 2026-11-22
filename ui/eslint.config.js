// ESLint for the UI. The rule that matters most is the HTML-sink ban
// (DESIGN-0004 § The UI, security): pull request titles, branch names,
// dashboard text and event summaries come from repositories, so nothing
// may reach the DOM as markup.
import js from "@eslint/js";
import reactHooks from "eslint-plugin-react-hooks";
import globals from "globals";
import tseslint from "typescript-eslint";

const sinkMessage = "Render text, never markup: repository-controlled values would become HTML (DESIGN-0004).";

export const htmlSinks = [
  { selector: "JSXAttribute[name.name='dangerouslySetInnerHTML']", message: sinkMessage },
  { selector: "Property[key.name='dangerouslySetInnerHTML']", message: sinkMessage },
  { selector: "AssignmentExpression[left.property.name=/^(innerHTML|outerHTML)$/]", message: sinkMessage },
  { selector: "CallExpression[callee.property.name='insertAdjacentHTML']", message: sinkMessage },
  { selector: "CallExpression[callee.object.name='document'][callee.property.name=/^(write|writeln)$/]", message: sinkMessage },
];

export default tseslint.config(
  { ignores: ["dist/**", "node_modules/**", "web/src/api/schema.gen.ts", "playwright-report/**", "test-results/**"] },
  js.configs.recommended,
  ...tseslint.configs.recommended,
  {
    rules: {
      "@typescript-eslint/no-unused-vars": ["error", { argsIgnorePattern: "^_", varsIgnorePattern: "^_", ignoreRestSiblings: true }],
    },
  },
  {
    files: ["web/**/*.{ts,tsx}"],
    languageOptions: { globals: globals.browser },
    plugins: { "react-hooks": reactHooks },
    rules: {
      "react-hooks/rules-of-hooks": "error",
      "react-hooks/exhaustive-deps": "warn",
      "no-restricted-syntax": ["error", ...htmlSinks],
      // The primitives are the design system's, on Base UI. A radix
      // import would add a second overlay stack with its own focus and
      // portals. zod stays out of the bundle: the package's problem and
      // list-search modules pull it in, and lib/errors and lib/list-params
      // do the same jobs without it.
      "no-restricted-imports": [
        "error",
        {
          paths: [
            { name: "radix-ui", message: "Use @donaldgifford/design-system's components (Base UI), not radix." },
            { name: "zod", message: "zod is kept out of the SPA bundle." },
            { name: "@donaldgifford/design-system/lib/problem", message: "Imports zod; use lib/errors (ApiError, parseProblem)." },
            { name: "@donaldgifford/design-system/lib/list-search", message: "Imports zod; use lib/list-params." },
          ],
          patterns: [{ group: ["@radix-ui/*"], message: "Use @donaldgifford/design-system's components (Base UI), not radix." }],
        },
      ],
    },
  },
  {
    files: ["server/**/*.ts", "scripts/**/*.ts", "e2e/**/*.ts", "*.config.ts", "eslint.config.js"],
    languageOptions: { globals: { ...globals.node, Bun: "readonly" } },
  },
);
