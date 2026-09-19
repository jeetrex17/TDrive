// One owner per edge, on the bottom sheets too.
//
// A sheet used to measure the keyboard itself with visualViewport, which is
// the one source that does not work on Android: once the activity takes the
// whole window nothing resizes the web view, so innerHeight and
// visualViewport.height both stay put and the sheet reserved nothing at all
// while the keys covered its text field. It now reads --inset-keyboard, the
// shared measurement, and nothing here may grow a second owner.

import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

const css = readFileSync(new URL('../../styles/modals.css', import.meta.url), 'utf8');
const shell = readFileSync(new URL('./ModalShell.svelte', import.meta.url), 'utf8');

/** The rules inside one selector's block, as written. */
function block(selector: string): string {
    const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
    return css.match(new RegExp(`${escaped} \\{[^}]*\\}`))?.[0] ?? '';
}

describe('the phone bottom sheet', () => {
    it('keeps no keyboard measurement of its own', () => {
        expect(shell).not.toContain('visualViewport');
        expect(shell).not.toContain('sheet-keyboard-inset');
        expect(css).not.toContain('sheet-keyboard-inset');
    });

    it('rises above the keyboard rather than padding itself down', () => {
        const sheet = block('html.mobile .modal-sheet');
        expect(sheet).toContain('margin-bottom: var(--inset-keyboard)');
        expect(sheet).toContain('max-height: calc(90dvh - var(--inset-keyboard))');
    });

    it('does not then reserve the same keyboard a second time inside', () => {
        // --inset-bottom folds the keyboard in, so a sheet already lifted by
        // it must subtract it back out or float a keyboard clear of the keys.
        const sheet = block('html.mobile .modal-sheet');
        expect(sheet).toContain('var(--inset-bottom) - var(--inset-keyboard)');
    });
});
