<script lang="ts">
    import { onDestroy, tick, type Snippet } from 'svelte';
    import { installModalA11y } from './modal-a11y';
    import { pushSheet, type SheetHandle } from './sheet-stack';
    import { createSheetDrag, sheetOffset, shouldDismiss, FLICK_SPEED } from './sheet-gesture';
    import { isMobilePlatform } from '../../api';

    interface Props {
        hostId: string;
        open: boolean;
        title?: string;
        titleId: string;
        subtitle?: string;
        cardClass?: string;
        // actionsClass restyles the footer wrapper for dialogs whose design
        // system class differs from the default .modal-actions.
        actionsClass?: string;
        // presentation decides the chrome: a centered dialog on desktop, a
        // bottom sheet on a phone. It defaults per platform in one place here;
        // a caller only passes it to force a surface (a dialog that would feel
        // wrong as a sheet, or the reverse).
        presentation?: 'dialog' | 'sheet';
        initialFocus?: string;
        restoreFocus?: string;
        onClose: () => void;
        // header replaces the default title/subtitle block for dialogs with a
        // custom heading layout. It must render an element with id={titleId}
        // so the card's aria-labelledby keeps pointing at the visible title.
        header?: Snippet;
        children?: Snippet;
        actions?: Snippet;
    }

    let {
        hostId,
        open,
        title = '',
        titleId,
        subtitle = '',
        cardClass = '',
        actionsClass = 'modal-actions',
        presentation,
        initialFocus,
        restoreFocus,
        onClose,
        header,
        children,
        actions,
    }: Props = $props();

    const EXIT_MS = 180;

    // Resolve the default once: phones get sheets, everything else dialogs.
    const asSheet = $derived((presentation ?? (isMobilePlatform() ? 'sheet' : 'dialog')) === 'sheet');

    let host = $state<HTMLElement | null>(null);
    let card = $state<HTMLElement | null>(null);
    let a11y: ReturnType<typeof installModalA11y> | null = null;
    let active = false;

    // rendered lags `open` while a sheet plays its close animation. On desktop
    // it tracks `open` exactly, so dialog mount/unmount timing is unchanged.
    let rendered = $state(false);
    let closing = $state(false);
    let dragging = $state(false);
    let wasOpen = false;
    let swipeExiting = false;
    let closeTimer: ReturnType<typeof setTimeout> | null = null;
    let sheetHistory: SheetHandle | null = null;

    let dragStartY = 0;
    const drag = createSheetDrag();
    let dragDelta = 0;

    function prefersReducedMotion(): boolean {
        return typeof window !== 'undefined'
            && typeof window.matchMedia === 'function'
            && window.matchMedia('(prefers-reduced-motion: reduce)').matches;
    }

    function initialFocusTarget(): Element | null {
        if (!initialFocus || !host) return null;
        return host.querySelector(initialFocus);
    }

    function ensureA11y(): ReturnType<typeof installModalA11y> | null {
        if (!host) return null;
        if (!a11y) {
            a11y = installModalA11y(host, {
                requestClose: () => onClose(),
                initialFocus: initialFocusTarget,
                restoreFocus,
            });
        }
        return a11y;
    }

    function handleHostClick(event: MouseEvent): void {
        if (event.target === host) onClose();
    }

    function mountHost(): void {
        if (host) return;
        host = document.getElementById(hostId);
        host?.addEventListener('click', handleHostClick);
    }

    function clearCloseTimer(): void {
        if (closeTimer === null) return;
        clearTimeout(closeTimer);
        closeTimer = null;
    }

    // visualViewport keeps the primary button above the on-screen keyboard: as
    // the keyboard opens the visual viewport shrinks, and the difference lifts
    // the sheet by that much. Hosts without visualViewport pad by nothing.
    function updateKeyboardInset(): void {
        if (!card) return;
        const vv = typeof window !== 'undefined' ? window.visualViewport : null;
        if (!vv) {
            card.style.setProperty('--sheet-keyboard-inset', '0px');
            return;
        }
        const inset = Math.max(0, window.innerHeight - vv.height - vv.offsetTop);
        card.style.setProperty('--sheet-keyboard-inset', `${inset}px`);
    }

    function attachViewport(): void {
        const vv = typeof window !== 'undefined' ? window.visualViewport : null;
        if (!asSheet || !vv) return;
        vv.addEventListener('resize', updateKeyboardInset);
        vv.addEventListener('scroll', updateKeyboardInset);
    }

    function detachViewport(): void {
        const vv = typeof window !== 'undefined' ? window.visualViewport : null;
        if (!vv) return;
        vv.removeEventListener('resize', updateKeyboardInset);
        vv.removeEventListener('scroll', updateKeyboardInset);
    }

    function setScrimProgress(value: number): void {
        host?.style.setProperty('--sheet-scrim', String(value));
    }

    function openTransition(): void {
        clearCloseTimer();
        swipeExiting = false;
        closing = false;
        dragging = false;
        dragDelta = 0;
        rendered = true;
        if (host) {
            host.style.display = 'flex';
            host.setAttribute('aria-hidden', 'false');
        }
        setScrimProgress(1);
        if (asSheet && !sheetHistory) {
            // One history entry per open sheet so Android BACK closes it first.
            sheetHistory = pushSheet(() => onClose());
        }
        active = true;
        void tick().then(() => {
            if (!open || !active) return;
            ensureA11y()?.activate();
            attachViewport();
            updateKeyboardInset();
        });
    }

    function finishClose(): void {
        rendered = false;
        closing = false;
        if (host) {
            host.style.display = 'none';
            host.setAttribute('aria-hidden', 'true');
        }
    }

    function closeTransition(): void {
        // Give up the history entry we pushed (pops it unless BACK already did).
        sheetHistory?.release();
        sheetHistory = null;
        detachViewport();
        if (active) {
            active = false;
            a11y?.deactivate();
        }
        if (host) host.setAttribute('aria-hidden', 'true');

        if (asSheet && !swipeExiting && !prefersReducedMotion()) {
            closing = true;
            clearCloseTimer();
            closeTimer = setTimeout(finishClose, EXIT_MS);
            return;
        }
        finishClose();
    }

    function sync(): void {
        mountHost();
        if (!host) return;
        host.classList.add('modal-overlay');
        host.classList.toggle('modal-overlay-sheet', asSheet);

        const isOpen = open;
        if (isOpen === wasOpen) {
            // No edge. Keep the host resolved to its current phase; the close
            // timer or a spring-back may have changed `rendered` underneath us.
            if (!isOpen && !rendered && !closing) {
                host.style.display = 'none';
                host.setAttribute('aria-hidden', 'true');
            }
            return;
        }
        wasOpen = isOpen;
        if (isOpen) openTransition();
        else closeTransition();
    }

    $effect(() => {
        // Track open (and asSheet, so a late platform resolution re-runs).
        void open;
        void asSheet;
        sync();
    });

    // --- swipe to dismiss (sheet only) ---------------------------------------

    function applyDrag(): void {
        if (!card) return;
        card.style.transform = `translateY(${dragDelta}px)`;
        const height = card.offsetHeight || 1;
        setScrimProgress(Math.max(0, 1 - dragDelta / height));
    }

    function springBack(velocity = 0): void {
        if (!card) return;
        // A finger that was still moving gets a shorter return, so the settle
        // continues the gesture instead of restarting from a standstill.
        const ms = velocity > FLICK_SPEED ? Math.round(EXIT_MS * 0.7) : EXIT_MS;
        card.style.transition = `transform ${ms}ms var(--ease-enter)`;
        card.style.transform = 'translateY(0)';
        setScrimProgress(1);
        const el = card;
        window.setTimeout(() => {
            el.style.transition = '';
            el.style.transform = '';
        }, EXIT_MS);
    }

    function swipeDismiss(): void {
        if (!card || prefersReducedMotion()) {
            onClose();
            return;
        }
        swipeExiting = true;
        card.style.transition = `transform ${EXIT_MS}ms var(--ease-standard)`;
        card.style.transform = 'translateY(100%)';
        setScrimProgress(0);
        clearCloseTimer();
        closeTimer = setTimeout(() => onClose(), EXIT_MS);
    }

    function onHandlePointerDown(event: PointerEvent): void {
        if (!asSheet || !card) return;
        dragging = true;
        dragStartY = event.clientY;
        dragDelta = 0;
        drag.start(event);
        card.style.transition = '';
        (event.currentTarget as HTMLElement).setPointerCapture?.(event.pointerId);
    }

    function onHandlePointerMove(event: PointerEvent): void {
        if (!dragging) return;
        // Follows the finger down, resists upward where the sheet is already open.
        dragDelta = sheetOffset(event.clientY - dragStartY, card?.offsetHeight ?? 0);
        drag.track(event);
        applyDrag();
    }

    function onHandlePointerUp(): void {
        if (!dragging) return;
        dragging = false;
        const height = card?.offsetHeight ?? 0;
        const threshold = Math.max(88, height * 0.28);
        // Judge the gesture by where it was going, not where it stopped.
        if (shouldDismiss(dragDelta, drag.velocity(), threshold)) swipeDismiss();
        else springBack(drag.velocity());
        dragDelta = 0;
        drag.reset();
    }

    onDestroy(() => {
        clearCloseTimer();
        sheetHistory?.release();
        sheetHistory = null;
        detachViewport();
        a11y?.deactivate();
        host?.removeEventListener('click', handleHostClick);
        if (host) {
            host.style.display = 'none';
            host.setAttribute('aria-hidden', 'true');
        }
    });
</script>

{#if open || rendered}
    <div
        bind:this={card}
        class={`modal-card ${cardClass}`.trim()}
        class:modal-sheet={asSheet}
        class:is-closing={closing}
        class:is-dragging={dragging}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
    >
        {#if asSheet}
            <div
                class="sheet-handle"
                aria-hidden="true"
                onpointerdown={onHandlePointerDown}
                onpointermove={onHandlePointerMove}
                onpointerup={onHandlePointerUp}
                onpointercancel={onHandlePointerUp}
            >
                <span></span>
            </div>
        {/if}

        {#if header}
            {@render header()}
        {:else}
            <h3 id={titleId} class="modal-title">{title}</h3>
            {#if subtitle}
                <p class="modal-subtitle">{subtitle}</p>
            {/if}
        {/if}

        {#if asSheet}
            <div class="modal-sheet-body">
                {@render children?.()}
            </div>
        {:else}
            {@render children?.()}
        {/if}

        {#if actions}
            <div class={actionsClass}>
                {@render actions()}
            </div>
        {/if}
    </div>
{/if}
