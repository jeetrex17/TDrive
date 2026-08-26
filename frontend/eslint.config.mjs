import js from "@eslint/js";
import globals from "globals";
import svelte from "eslint-plugin-svelte";
import tseslint from "typescript-eslint";

export default tseslint.config(
    { ignores: ["coverage/", "dist/", "wailsjs/", "node_modules/"] },
    js.configs.recommended,
    ...tseslint.configs.recommended,
    ...svelte.configs["flat/recommended"],
    {
        languageOptions: {
            ecmaVersion: 2022,
            sourceType: "module",
            globals: { ...globals.browser },
        },
        rules: {
            // Surface, don't fail, on pre-existing legacy patterns. Tighten to
            // "error" as modules get migrated onto the typed boundary.
            "no-unused-vars": "off",
            "@typescript-eslint/no-unused-vars": ["warn", { argsIgnorePattern: "^_" }],
            "no-empty": ["warn", { allowEmptyCatch: true }],
            // `any` is a deliberate bridge at the JS<->TS boundary (untyped
            // Wails globals, loosely-shaped transient state) during the gradual
            // migration. Surface it, don't fail on it.
            "@typescript-eslint/no-explicit-any": "warn",
        },
    },
    {
        files: ["**/*.test.{js,ts}"],
        languageOptions: { globals: { ...globals.node } },
    },
    {
        files: ["**/*.svelte"],
        languageOptions: {
            parserOptions: {
                parser: tseslint.parser,
            },
        },
    },
);
