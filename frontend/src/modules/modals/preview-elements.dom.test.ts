import { beforeEach, describe, expect, it } from 'vitest';
import { previewElementsLive, resolvePreviewElements } from './preview-elements';

const FULL_MARKUP = `
    <div id="preview-shell">
        <div id="preview-filename"></div>
        <button id="preview-info-btn" type="button"></button>
        <button id="preview-download" type="button"></button>
        <button id="preview-close" type="button"></button>
        <button id="preview-prev" type="button"></button>
        <button id="preview-next" type="button"></button>
        <div id="preview-counter"></div>
        <div id="preview-stage">
            <div id="preview-loading"><div id="preview-loading-fill"></div></div>
            <div id="preview-error"></div>
            <img id="preview-image" alt="Preview">
        </div>
        <aside id="preview-info"><button id="preview-info-close" type="button"></button><div id="preview-info-body"></div></aside>
        <div id="preview-locked">
            <input id="preview-locked-input" type="password">
            <button id="preview-locked-eye" type="button"></button>
            <button id="preview-locked-unlock" type="button"></button>
            <div id="preview-locked-error"></div>
            <div id="preview-locked-hint"><span id="preview-locked-hint-text"></span></div>
        </div>
    </div>`;

function host(inner = FULL_MARKUP): HTMLElement {
    document.body.innerHTML = `<div id="preview-modal">${inner}</div>`;
    return document.getElementById('preview-modal') as HTMLElement;
}

beforeEach(() => {
    document.body.innerHTML = '';
});

describe("resolving the preview modal's elements", () => {
    it('hands back every element the controller writes to', () => {
        const resolved = resolvePreviewElements(host());
        expect('elements' in resolved).toBe(true);
        if (!('elements' in resolved)) return;

        const els = resolved.elements;
        expect(els.modal.id).toBe('preview-modal');
        expect(els.image.tagName).toBe('IMG');
        expect(els.lockedInput?.type).toBe('password');
        expect(els.counter?.id).toBe('preview-counter');
    });

    it('names the missing ones rather than reporting success with holes in it', () => {
        const resolved = resolvePreviewElements(host('<div id="preview-shell"></div>'));
        expect('missing' in resolved).toBe(true);
        if (!('missing' in resolved)) return;

        expect(resolved.missing).toContain('preview-image');
        expect(resolved.missing).toContain('preview-error');
        expect(resolved.missing).not.toContain('preview-shell');
    });

    it('accepts markup with no info panel and no unlock card, which the renditions view has', () => {
        const resolved = resolvePreviewElements(host(`
            <div id="preview-shell">
                <div id="preview-filename"></div>
                <button id="preview-close" type="button"></button>
                <div id="preview-stage">
                    <div id="preview-loading"><div id="preview-loading-fill"></div></div>
                    <div id="preview-error"></div>
                    <img id="preview-image" alt="">
                </div>
            </div>`));
        expect('elements' in resolved).toBe(true);
        if (!('elements' in resolved)) return;

        expect(resolved.elements.infoPanel).toBeNull();
        expect(resolved.elements.locked).toBeNull();
        expect(resolved.elements.downloadButton).toBeNull();
    });

    it('takes only elements from the host it was given, not whatever else the page has', () => {
        document.body.innerHTML = `<div id="stray"><img id="preview-image" alt="stray"></div><div id="preview-modal">${FULL_MARKUP}</div>`;
        const modal = document.getElementById('preview-modal') as HTMLElement;
        const resolved = resolvePreviewElements(modal);
        if (!('elements' in resolved)) throw new Error('expected the full markup to resolve');

        expect(resolved.elements.image.alt).toBe('Preview');
    });
});

describe('deciding whether a resolved set is still on screen', () => {
    it('says yes while the markup it resolved from is still mounted', () => {
        const resolved = resolvePreviewElements(host());
        if (!('elements' in resolved)) throw new Error('expected the full markup to resolve');

        expect(previewElementsLive(resolved.elements)).toBe(true);
    });

    it('says no once the shell has been replaced under a host that stayed put', () => {
        const modal = host();
        const resolved = resolvePreviewElements(modal);
        if (!('elements' in resolved)) throw new Error('expected the full markup to resolve');

        // A re-render leaves the old elements detached: writing to them would
        // land nowhere and the preview would look frozen rather than broken.
        modal.innerHTML = FULL_MARKUP;
        expect(previewElementsLive(resolved.elements)).toBe(false);
    });
});
