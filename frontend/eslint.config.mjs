import js from "@eslint/js";
import globals from "globals";
import tseslint from "typescript-eslint";

export default tseslint.config(
    { ignores: ["dist/", "wailsjs/", "node_modules/"] },
    js.configs.recommended,
    ...tseslint.configs.recommended,
    {
        languageOptions: {
            ecmaVersion: 2022,
            sourceType: "module",
            globals: { ...globals.browser },
        },
        rules: {
            // Surface, don't fail, on pre-existing legacy patterns. Tighten to
            // "error" as modules get migrated onto the typed boundary.
            "no-unused-vars": ["warn", { argsIgnorePattern: "^_" }],
            "@typescript-eslint/no-unused-vars": ["warn", { argsIgnorePattern: "^_" }],
            "no-empty": ["warn", { allowEmptyCatch: true }],
        },
    },
    {
        files: ["**/*.test.{js,ts}"],
        languageOptions: { globals: { ...globals.node } },
    },
);
