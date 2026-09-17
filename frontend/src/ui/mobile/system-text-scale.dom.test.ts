import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const isIOSPlatform = vi.hoisted(() => vi.fn(() => true));
const isAndroidPlatform = vi.hoisted(() => vi.fn(() => false));
vi.mock('../../api', () => ({ isAndroidPlatform, isIOSPlatform }));

import {
    SYSTEM_TEXT_SCALE_EVENT,
    activateSystemTextScale,
} from './system-text-scale';

declare global {
    interface Window {
        __tdriveSystemTextScale?: unknown;
    }
}

beforeEach(() => {
    isIOSPlatform.mockReturnValue(true);
    isAndroidPlatform.mockReturnValue(false);
    delete window.__tdriveSystemTextScale;
    document.documentElement.style.removeProperty('font-size');
});

afterEach(() => {
    vi.restoreAllMocks();
    document.documentElement.style.removeProperty('font-size');
    delete window.__tdriveSystemTextScale;
});

describe('activateSystemTextScale', () => {
    it('uses the iOS host scale that arrived before the app mounted', () => {
        window.__tdriveSystemTextScale = 1.35;

        const dispose = activateSystemTextScale();

        expect(document.documentElement.style.fontSize).toBe('21.6px');
        expect(document.documentElement.dataset.mobileTextScale).toBe('regular');
        dispose();
    });

    it('updates live when Dynamic Type changes without accepting malformed host data', () => {
        const dispose = activateSystemTextScale();
        window.dispatchEvent(new CustomEvent(SYSTEM_TEXT_SCALE_EVENT, { detail: { scale: 1.5 } }));
        expect(document.documentElement.style.fontSize).toBe('24px');
        expect(document.documentElement.dataset.mobileTextScale).toBe('large');

        window.dispatchEvent(new CustomEvent(SYSTEM_TEXT_SCALE_EVENT, { detail: { scale: 'huge' } }));
        expect(document.documentElement.style.fontSize).toBe('24px');
        dispose();
    });

    it('bounds an extreme native value so accessibility settings cannot break the layout', () => {
        const dispose = activateSystemTextScale();
        window.dispatchEvent(new CustomEvent(SYSTEM_TEXT_SCALE_EVENT, { detail: { scale: 9 } }));
        expect(document.documentElement.style.fontSize).toBe('40px');
        expect(document.documentElement.dataset.mobileTextScale).toBe('large');
        dispose();
    });

    it('uses the Android host scale for the large-text layout without overriding WebView zoom', () => {
        isIOSPlatform.mockReturnValue(false);
        isAndroidPlatform.mockReturnValue(true);
        window.__tdriveSystemTextScale = 1.7;

        const dispose = activateSystemTextScale();

        expect(document.documentElement.style.fontSize).toBe('');
        expect(document.documentElement.dataset.mobileTextScale).toBe('large');
        dispose();
    });

    it('restores a pre-existing root font size when the phone shell unmounts', () => {
        document.documentElement.style.fontSize = '18px';
        window.__tdriveSystemTextScale = 1.25;

        const dispose = activateSystemTextScale();
        expect(document.documentElement.style.fontSize).toBe('20px');

        dispose();
        expect(document.documentElement.style.fontSize).toBe('18px');
    });
});
