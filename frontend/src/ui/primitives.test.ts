import { describe, expect, it } from 'vitest';
import { Button, IconButton, ProgressBar, StateView, mountSvelte } from '.';

describe('Svelte UI primitives', () => {
    it('exports stable component entry points', () => {
        expect(Button).toBeTruthy();
        expect(IconButton).toBeTruthy();
        expect(ProgressBar).toBeTruthy();
        expect(StateView).toBeTruthy();
    });

    it('exports a hybrid mount helper', () => {
        expect(typeof mountSvelte).toBe('function');
    });
});
