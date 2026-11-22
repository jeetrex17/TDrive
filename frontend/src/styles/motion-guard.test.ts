// Interface transitions run on the shared motion tokens (--motion-fast, -med,
// -slow and --ease-standard), so every hover, press and reveal in the app moves
// at the same pace. A raw "140ms ease" here drifts the feel one control at a
// time. Long loops (spinners, shimmer) are animations, not transitions, and
// keep their own timing.

import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join, relative } from 'node:path';
import { describe, expect, it } from 'vitest';

const SRC = join(__dirname, '..');

function sourceFiles(dir: string): string[] {
    return readdirSync(dir).flatMap((name) => {
        const path = join(dir, name);
        if (statSync(path).isDirectory()) return sourceFiles(path);
        return /\.(css|svelte)$/.test(name) ? [path] : [];
    });
}

describe('motion', () => {
    it('keeps interface transitions on the shared motion tokens', () => {
        const offenders: string[] = [];
        for (const file of sourceFiles(SRC)) {
            const source = readFileSync(file, 'utf8').replace(/\/\*[\s\S]*?\*\//g, '');
            for (const match of source.matchAll(/transition(?:-duration|-timing-function)?\s*:([^;{}]*);/g)) {
                const value = match[1];
                const rawDuration = [...value.matchAll(/(?<![\w.-])(\d*\.?\d+)(m?s)\b/g)]
                    .some(([, amount, unit]) => (unit === 's' ? Number(amount) * 1000 : Number(amount)) >= 1);
                const rawEasing = /(?<![\w-])(ease|ease-in|ease-out|ease-in-out)(?![\w-])/.test(value);
                if (rawDuration || rawEasing) offenders.push(`${relative(SRC, file)}: transition:${value.trim()}`);
            }
        }
        expect(offenders).toEqual([]);
    });
});
