// Hover styles stay behind @media (hover: hover). A touch screen reports a
// tap as a hover that never ends, so an unguarded :hover leaves a tapped row
// or button highlighted on iOS until something else is touched.

import { readFileSync, readdirSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';

const STYLES = __dirname;

/** Selectors of rules that sit outside any at-rule. */
function topLevelSelectors(css: string): string[] {
    const source = css.replace(/\/\*[\s\S]*?\*\//g, '');
    const selectors: string[] = [];
    let depth = 0;
    let start = 0;
    for (let index = 0; index < source.length; index += 1) {
        const char = source[index];
        if (char === '{') {
            if (depth === 0) selectors.push(source.slice(start, index).trim());
            depth += 1;
        } else if (char === '}') {
            depth -= 1;
            if (depth === 0) start = index + 1;
        } else if (char === ';' && depth === 0) {
            start = index + 1;
        }
    }
    return selectors;
}

describe('stylesheets', () => {
    it('keep every :hover rule behind a hover media query', () => {
        const unguarded = readdirSync(STYLES)
            .filter((name) => name.endsWith('.css'))
            .flatMap((name) => topLevelSelectors(readFileSync(join(STYLES, name), 'utf8'))
                .filter((selector) => !selector.startsWith('@') && selector.includes(':hover'))
                .map((selector) => `${name}: ${selector}`));
        expect(unguarded).toEqual([]);
    });
});
