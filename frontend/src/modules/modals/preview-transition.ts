const REDUCED_MOTION_QUERY = '(prefers-reduced-motion: reduce)';
const OPEN_WATCHDOG_MS = 720;

type PreviewRect = Pick<DOMRectReadOnly, 'left' | 'top' | 'width' | 'height'>;

export interface PreviewTransitionSource {
    element: HTMLElement;
    imageSrc: string;
    rect: PreviewRect;
    borderRadius: string;
}

export interface PreviewTransitionController {
    beginOpen(source: PreviewTransitionSource | null, overlay: HTMLElement): boolean;
    finishOpen(target: HTMLImageElement): boolean;
    playClose(source: PreviewTransitionSource | null, target: HTMLImageElement): boolean;
    cancel(): void;
    isRunning(): boolean;
}

interface ActiveVisual {
    id: number;
    clone: HTMLImageElement;
    startRect: PreviewRect;
    overlay: HTMLElement | null;
    target: HTMLImageElement | null;
    previousTargetOpacity: string;
    animations: Animation[];
    finishScheduled: boolean;
    watchdog: number | undefined;
}

function copyRect(rect: DOMRectReadOnly): PreviewRect {
    return {
        left: rect.left,
        top: rect.top,
        width: rect.width,
        height: rect.height,
    };
}

function isUsableRect(rect: PreviewRect): boolean {
    if (![rect.left, rect.top, rect.width, rect.height].every(Number.isFinite)) return false;
    if (rect.width <= 0 || rect.height <= 0) return false;

    return rect.left + rect.width > 0
        && rect.top + rect.height > 0
        && rect.left < window.innerWidth
        && rect.top < window.innerHeight;
}

function readDuration(token: string, fallback: number): number {
    const raw = getComputedStyle(document.documentElement).getPropertyValue(token).trim();
    if (!raw) return fallback;

    const value = Number.parseFloat(raw);
    if (!Number.isFinite(value)) return fallback;
    return raw.endsWith('s') && !raw.endsWith('ms') ? value * 1000 : value;
}

function createVisualClone(
    src: string,
    rect: PreviewRect,
    borderRadius: string,
    objectFit: 'cover' | 'contain',
): HTMLImageElement {
    const clone = document.createElement('img');
    clone.className = 'preview-shared-transition-image';
    clone.src = src;
    clone.alt = '';
    clone.setAttribute('aria-hidden', 'true');
    clone.draggable = false;
    clone.style.left = `${rect.left}px`;
    clone.style.top = `${rect.top}px`;
    clone.style.width = `${rect.width}px`;
    clone.style.height = `${rect.height}px`;
    clone.style.borderRadius = borderRadius;
    clone.style.objectFit = objectFit;
    document.body.appendChild(clone);
    return clone;
}

export function capturePreviewTransitionSource(element: HTMLElement | null): PreviewTransitionSource | null {
    if (!element?.isConnected) return null;

    const image = element.querySelector<HTMLImageElement>('.gallery-thumb');
    const imageSrc = image?.currentSrc || image?.src || '';
    const rect = copyRect(element.getBoundingClientRect());
    if (!imageSrc || !isUsableRect(rect)) return null;

    return {
        element,
        imageSrc,
        rect,
        borderRadius: getComputedStyle(element).borderRadius || '0px',
    };
}

export function createPreviewTransitionController(): PreviewTransitionController {
    let generation = 0;
    let active: ActiveVisual | null = null;
    const mediaQuery = typeof window.matchMedia === 'function'
        ? window.matchMedia(REDUCED_MOTION_QUERY)
        : null;

    function clearVisual(visual: ActiveVisual): void {
        clearTimeout(visual.watchdog);
        visual.animations.forEach((animation) => animation.cancel());
        if (visual.target) visual.target.style.opacity = visual.previousTargetOpacity;
        visual.overlay?.classList.remove('is-shared-entering');
        visual.clone.remove();
    }

    function finish(id: number): void {
        if (!active || active.id !== id) return;
        const visual = active;
        active = null;
        clearVisual(visual);
    }

    function cancel(): void {
        generation += 1;
        if (!active) return;
        const visual = active;
        active = null;
        clearVisual(visual);
    }

    function settleAfterAnimations(visual: ActiveVisual): void {
        Promise.all(visual.animations.map((animation) => animation.finished.catch(() => undefined)))
            .then(() => finish(visual.id));
    }

    function beginOpen(source: PreviewTransitionSource | null, overlay: HTMLElement): boolean {
        cancel();
        const motionAllowed = !mediaQuery?.matches
            && typeof document.createElement('div').animate === 'function';
        if (!source || !source.element.isConnected || !isUsableRect(source.rect) || !motionAllowed) return false;

        const clone = createVisualClone(source.imageSrc, source.rect, source.borderRadius, 'cover');
        const visual: ActiveVisual = {
            id: ++generation,
            clone,
            startRect: source.rect,
            overlay,
            target: null,
            previousTargetOpacity: '',
            animations: [],
            finishScheduled: false,
            watchdog: undefined,
        };

        active = visual;
        overlay.classList.add('is-shared-entering');
        visual.watchdog = window.setTimeout(() => finish(visual.id), OPEN_WATCHDOG_MS);
        return true;
    }

    function finishOpen(target: HTMLImageElement): boolean {
        const visual = active;
        if (!visual || visual.finishScheduled) return Boolean(visual);

        visual.finishScheduled = true;
        visual.target = target;
        visual.previousTargetOpacity = target.style.opacity;
        target.style.opacity = '0';

        const animateToTarget = () => {
            if (!active || active.id !== visual.id) return;
            if (!target.complete || target.naturalWidth <= 0) {
                target.addEventListener('load', animateToTarget, { once: true });
                return;
            }

            const targetRect = copyRect(target.getBoundingClientRect());
            if (!isUsableRect(targetRect)) {
                finish(visual.id);
                return;
            }

            clearTimeout(visual.watchdog);
            visual.watchdog = undefined;

            const sourceRect = visual.startRect;
            const translateX = targetRect.left - sourceRect.left;
            const translateY = targetRect.top - sourceRect.top;
            const scaleX = targetRect.width / sourceRect.width;
            const scaleY = targetRect.height / sourceRect.height;
            const destination = `translate3d(${translateX}px, ${translateY}px, 0) scale3d(${scaleX}, ${scaleY}, 1)`;
            const duration = readDuration('--motion-slow', 240);
            const easing = getComputedStyle(document.documentElement)
                .getPropertyValue('--ease-enter').trim() || 'cubic-bezier(0.16, 1, 0.3, 1)';
            const targetRadius = getComputedStyle(target).borderRadius || '0px';

            visual.animations = [
                visual.clone.animate(
                    [
                        { opacity: 1, transform: 'translate3d(0, 0, 0) scale3d(1, 1, 1)', borderRadius: visual.clone.style.borderRadius },
                        { opacity: 1, transform: destination, borderRadius: targetRadius, offset: 0.72 },
                        { opacity: 0, transform: destination, borderRadius: targetRadius },
                    ],
                    { duration, easing, fill: 'both' },
                ),
                target.animate(
                    [
                        { opacity: 0 },
                        { opacity: 0, offset: 0.45 },
                        { opacity: 1 },
                    ],
                    { duration, easing, fill: 'both' },
                ),
            ];
            settleAfterAnimations(visual);
        };

        animateToTarget();
        return true;
    }

    function playClose(source: PreviewTransitionSource | null, target: HTMLImageElement): boolean {
        cancel();
        const motionAllowed = !mediaQuery?.matches
            && typeof document.createElement('div').animate === 'function';
        if (!source?.element.isConnected || (!target.currentSrc && !target.src) || !motionAllowed) return false;

        const sourceRect = copyRect(source.element.getBoundingClientRect());
        const targetRect = copyRect(target.getBoundingClientRect());
        if (!isUsableRect(sourceRect) || !isUsableRect(targetRect)) return false;

        const clone = createVisualClone(
            target.currentSrc || target.src,
            targetRect,
            getComputedStyle(target).borderRadius || '0px',
            'contain',
        );
        const visual: ActiveVisual = {
            id: ++generation,
            clone,
            startRect: targetRect,
            overlay: null,
            target: null,
            previousTargetOpacity: '',
            animations: [],
            finishScheduled: true,
            watchdog: undefined,
        };
        active = visual;

        const translateX = sourceRect.left - targetRect.left;
        const translateY = sourceRect.top - targetRect.top;
        const scaleX = sourceRect.width / targetRect.width;
        const scaleY = sourceRect.height / targetRect.height;
        const destination = `translate3d(${translateX}px, ${translateY}px, 0) scale3d(${scaleX}, ${scaleY}, 1)`;
        const duration = readDuration('--motion-med', 180);
        const easing = getComputedStyle(document.documentElement)
            .getPropertyValue('--ease-standard').trim() || 'cubic-bezier(0.2, 0, 0, 1)';

        visual.animations = [clone.animate(
            [
                { opacity: 1, transform: 'translate3d(0, 0, 0) scale3d(1, 1, 1)', borderRadius: clone.style.borderRadius },
                { opacity: 0.9, transform: destination, borderRadius: source.borderRadius, offset: 0.68 },
                { opacity: 0, transform: destination, borderRadius: source.borderRadius },
            ],
            { duration, easing, fill: 'both' },
        )];
        settleAfterAnimations(visual);
        return true;
    }

    mediaQuery?.addEventListener?.('change', (event) => {
        if (event.matches) cancel();
    });

    return {
        beginOpen,
        finishOpen,
        playClose,
        cancel,
        isRunning: () => active !== null,
    };
}
