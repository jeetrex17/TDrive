// Records whether the reader last used the keyboard or a pointer, on <html> as
// data-input. The file list moves focus to a row by script so the arrow keys
// work at once; browsers count that as keyboard focus before any input, which
// paints a ring nobody asked for. Styles that should follow the reader's own
// hand read this attribute instead of :focus-visible alone.

const NAVIGATION_KEYS = new Set(['Tab', 'ArrowUp', 'ArrowDown', 'ArrowLeft', 'ArrowRight', 'Home', 'End', 'PageUp', 'PageDown', 'Enter', ' ']);

export function trackInputModality(root: HTMLElement = document.documentElement): () => void {
    const onKey = (event: KeyboardEvent) => {
        if (NAVIGATION_KEYS.has(event.key)) root.dataset.input = 'keyboard';
    };
    const onPointer = () => {
        root.dataset.input = 'pointer';
    };
    window.addEventListener('keydown', onKey, true);
    window.addEventListener('pointerdown', onPointer, true);
    return () => {
        window.removeEventListener('keydown', onKey, true);
        window.removeEventListener('pointerdown', onPointer, true);
        delete root.dataset.input;
    };
}
