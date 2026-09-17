// Dynamic Type is a host setting, not a browser zoom setting. The iOS host
// publishes the current UIFontMetrics multiplier before the app mounts and
// whenever the user's preferred content size changes. Scaling the root keeps
// rem-based typography responsive without enlarging the shell's hit targets.

import { isAndroidPlatform, isIOSPlatform } from '../../api';

export const SYSTEM_TEXT_SCALE_EVENT = 'tdrive:system-text-scale';

const DEFAULT_ROOT_FONT_SIZE_PX = 16;
const MIN_SCALE = 0.8;
const MAX_SCALE = 2.5;
const LARGE_TEXT_SCALE = 1.5;

declare global {
    interface Window {
        __tdriveSystemTextScale?: unknown;
    }
}

function parseScale(value: unknown): number | null {
    const candidate = typeof value === 'number'
        ? value
        : value !== null && typeof value === 'object' && 'scale' in value
            ? (value as { scale?: unknown }).scale
            : null;
    if (typeof candidate !== 'number' || !Number.isFinite(candidate) || candidate <= 0) return null;
    return Math.min(MAX_SCALE, Math.max(MIN_SCALE, candidate));
}

/**
 * Keeps rem typography in step with iOS Dynamic Type for as long as the phone
 * shell exists. Android uses WebSettings.setTextZoom instead, so the two hosts
 * cannot multiply the same preference.
 */
export function activateSystemTextScale(): () => void {
    if (typeof window === 'undefined') return () => {};
    const ios = isIOSPlatform();
    if (!ios && !isAndroidPlatform()) return () => {};

    const root = document.documentElement.style;
    const documentRoot = document.documentElement;
    const previous = root.getPropertyValue('font-size');
    const previousPriority = root.getPropertyPriority('font-size');
    const previousScaleCategory = documentRoot.dataset.mobileTextScale;

    const apply = (value: unknown): void => {
        const scale = parseScale(value);
        if (scale === null) return;
        // Android's host maps this preference through WebSettings.setTextZoom.
        // It still publishes the category so its wrapping rules match iOS, but
        // must not multiply that native zoom by changing the root as well.
        if (ios) root.setProperty('font-size', `${DEFAULT_ROOT_FONT_SIZE_PX * scale}px`);
        documentRoot.dataset.mobileTextScale = scale >= LARGE_TEXT_SCALE ? 'large' : 'regular';
    };
    const onChange = (event: Event): void => {
        apply((event as CustomEvent<unknown>).detail);
    };

    apply(window.__tdriveSystemTextScale);
    window.addEventListener(SYSTEM_TEXT_SCALE_EVENT, onChange);

    return () => {
        window.removeEventListener(SYSTEM_TEXT_SCALE_EVENT, onChange);
        if (ios) {
            if (previous) root.setProperty('font-size', previous, previousPriority);
            else root.removeProperty('font-size');
        }
        if (previousScaleCategory === undefined) delete documentRoot.dataset.mobileTextScale;
        else documentRoot.dataset.mobileTextScale = previousScaleCategory;
    };
}
