declare module 'markdown-it' {
    interface Token {
        attrs: Array<[string, string]> | null;
        attrIndex(name: string): number;
        attrSet(name: string, value: string): void;
    }

    export type RenderRule = (
        tokens: Token[], index: number, options: unknown, env: unknown, renderer: Renderer,
    ) => string;

    interface Renderer {
        rules: Record<string, RenderRule | undefined>;
        renderToken(tokens: Token[], index: number, options: unknown): string;
    }

    // The viewer uses this renderer surface; plugins are intentionally absent.
    export default class MarkdownIt {
        constructor(options?: { html?: boolean; linkify?: boolean; typographer?: boolean });
        renderer: Renderer;
        render(source: string): string;
    }
}
