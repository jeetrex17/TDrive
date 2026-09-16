import { readdirSync, readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { join, relative, sep } from 'node:path';
import { describe, expect, it } from 'vitest';

const sourceRoot = fileURLToPath(new URL('..', import.meta.url));

function productionSourceFiles(directory: string): string[] {
    return readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
        const path = join(directory, entry.name);
        if (entry.isDirectory()) return productionSourceFiles(path);
        if (!entry.name.endsWith('.svelte') && !entry.name.endsWith('.ts')) return [];
        if (entry.name.endsWith('.test.ts')) return [];
        return [path];
    });
}

// Every glyph in the app comes from Lucide, so the icon set stays one family
// at one stroke weight. The exceptions are listed exactly rather than allowed
// by pattern, so adding a hand-drawn SVG is a deliberate act that shows up in
// review instead of quietly widening the rule.
const ALLOWED_RAW_SVG = [
    // The skip-10 controls draw an arc with a numeral inside it, which has no
    // Lucide equivalent.
    'ui/video/VideoModal.svelte',
    'ui/video/VideoModal.svelte',
    'ui/video/VideoModal.svelte',
    'ui/video/VideoModal.svelte',
];

describe('icon system', () => {
    it('uses Lucide except for the exact skip-10 controls', () => {
        const rawSvgLocations = productionSourceFiles(sourceRoot).flatMap((path) => {
            const count = readFileSync(path, 'utf8').match(/<svg\b/g)?.length ?? 0;
            const displayPath = relative(sourceRoot, path).split(sep).join('/');
            return Array.from({ length: count }, () => displayPath);
        });

        expect(rawSvgLocations).toEqual(ALLOWED_RAW_SVG);
    });
});
