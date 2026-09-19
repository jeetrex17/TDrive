// One owner per edge, on the sign-in pages too.
//
// Signing in used to run a keyboard measurement of its own next to the phone
// shell's, so the bottom of the page was reserved twice: the card shrank by a
// keyboard and the action bar padded by another, which squeezed the scrolling
// body until the focused field had nowhere to go but under the status bar.
// These pages now reserve by the same --inset-top / --inset-bottom the rest of
// the shell uses, and nothing here may grow a second owner.

import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

const css = readFileSync(new URL('../../styles/auth.css', import.meta.url), 'utf8');
const screens = readFileSync(new URL('./AuthScreens.svelte', import.meta.url), 'utf8');
const drive = readFileSync(new URL('./PersonalDriveSetup.svelte', import.meta.url), 'utf8');

describe('the phone sign-in pages', () => {
    it('keep no keyboard measurement of their own', () => {
        expect(css).not.toContain('--auth-keyboard-inset');
        expect(screens).not.toContain('keyboardInset');
        expect(drive).not.toContain('keyboardInset');
        for (const source of [screens, drive]) {
            expect(source).not.toContain('visualViewport');
        }
    });

    it('end where the keyboard starts, and pad inside by the screen edge alone', () => {
        const box = css.match(/html\.mobile \.auth-box \{[^}]*\}/)?.[0] ?? '';
        expect(box).toContain('height: calc(100% - var(--inset-keyboard))');
        expect(box).toContain('overflow-y: auto');
        // Reserving --inset-bottom inside a page that already ends at the keys
        // is the double count this whole layout exists to avoid.
        const actions = css.match(/html\.mobile \.auth-actions \{[^}]*\}/)?.[0] ?? '';
        expect(actions).toContain('calc(var(--inset-bottom) - var(--inset-keyboard) + 12px)');
    });

    it('reserve the status bar and stop the scroll short of it', () => {
        const box = css.match(/html\.mobile \.auth-box \{[^}]*\}/)?.[0] ?? '';
        expect(box).toContain('scroll-padding-top: calc(var(--inset-top) + 8px)');
        const body = css.match(/html\.mobile \.auth-page-body \{[^}]*\}/)?.[0] ?? '';
        expect(body).toContain('padding: calc(var(--inset-top) + 40px)');
        // Grows into a roomy page, never shrinks below its content in a cramped
        // one, which is what keeps the button docked without crushing the form.
        expect(body).toContain('flex: 1 0 auto');
    });

    it('name the keyboard on its own so a page can subtract what it already took', () => {
        const tokens = readFileSync(new URL('../../styles/tokens.css', import.meta.url), 'utf8');
        expect(tokens).toContain('--inset-keyboard: var(--mobile-keyboard-inset, 0px)');
        expect(tokens).toMatch(/--inset-bottom: max\([^;]*var\(--inset-keyboard\)[^;]*\);/);
    });

    it('bring a field’s label along when the keyboard scrolls it into view', () => {
        expect(css).toMatch(/html\.mobile \.auth-field input \{[^}]*scroll-margin-top:/);
    });
});
