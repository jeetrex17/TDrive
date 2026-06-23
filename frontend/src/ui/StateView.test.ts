import { describe, expect, it } from 'vitest';
import StateView from './StateView.svelte';

describe('StateView', () => {
    it('is compiled by the Svelte toolchain', () => {
        expect(StateView).toBeTruthy();
    });
});
