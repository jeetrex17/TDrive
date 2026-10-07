<script lang="ts">
    import { onDestroy, tick, type Snippet } from 'svelte';
    import { installModalA11y } from './modal-a11y';
    import { createSheetDragController, FLICK_SPEED } from './sheet-gesture';
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

    function setScrimProgress(value: number): void {
        host?.style.setProperty('--sheet-scrim', String(value));
    }

    function openTransition(): void {
        clearCloseTimer();
        swipeExiting = false;
        closing = false;
        dragging = false;
        rendered = true;
        if (host) {
            host.style.display = 'flex';
            host.setAttribute('aria-hidden', 'false');
        }
        setScrimProgress(1);
        active = true;
        void tick().then(() => {
            if (!open || !active) return;
            ensureA11y()?.activate();
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

    // The pointer wiring is the shared one; this host only says how to follow
    // the finger (scrim), settle and dismiss. The grab area is the header, so a
    // press on a header button falls through to the button.
    const sheetDrag = createSheetDragController({
        sheet: () => card,
        grab: (el) => {
            dragging = true;
            el.style.transition = '';
        },
        track: (offset, height) => setScrimProgress(Math.max(0, 1 - offset / height)),
        settle: (velocity) => {
            dragging = false;
            springBack(velocity);
        },
        dismiss: () => {
            dragging = false;
            swipeDismiss();
        },
    });

    onDestroy(() => {
        clearCloseTimer();
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
        {#snippet titleBlock()}
            {#if header}
                {@render header()}
            {:else}
                <h3 id={titleId} class="modal-title">{title}</h3>
                {#if subtitle}
                    <p class="modal-subtitle">{subtitle}</p>
                {/if}
            {/if}
        {/snippet}

        {#if asSheet}
            <!-- The grab area is the grip plus the title row, so the whole top of
                 the sheet drags. A press on a header control falls through to it. -->
            <div
                class="sheet-grab"
                role="presentation"
                onpointerdown={sheetDrag.onPointerDown}
                onpointermove={sheetDrag.onPointerMove}
                onpointerup={sheetDrag.onPointerUp}
                onpointercancel={sheetDrag.onPointerUp}
            >
                <div class="sheet-handle" aria-hidden="true"><span></span></div>
                {@render titleBlock()}
            </div>
        {:else}
            {@render titleBlock()}
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
