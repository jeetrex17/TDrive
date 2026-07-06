<script lang="ts">
    import { onDestroy, tick, type Snippet } from 'svelte';
    import { installModalA11y } from './modal-a11y';

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
        initialFocus,
        restoreFocus,
        onClose,
        header,
        children,
        actions,
    }: Props = $props();

    let host = $state<HTMLElement | null>(null);
    let a11y: ReturnType<typeof installModalA11y> | null = null;
    let active = false;

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

    async function syncOpenState(): Promise<void> {
        mountHost();
        if (!host) return;

        host.classList.add('modal-overlay');
        host.style.display = open ? 'flex' : 'none';
        host.setAttribute('aria-hidden', open ? 'false' : 'true');

        if (open && !active) {
            active = true;
            const shouldActivate = open;
            await tick();
            if (shouldActivate && open && active) {
                ensureA11y()?.activate();
            }
            return;
        }

        if (!open && active) {
            active = false;
            a11y?.deactivate();
        }
    }

    $effect(() => {
        void syncOpenState();
    });

    onDestroy(() => {
        a11y?.deactivate();
        host?.removeEventListener('click', handleHostClick);
        if (host) {
            host.style.display = 'none';
            host.setAttribute('aria-hidden', 'true');
        }
    });
</script>

{#if open}
    <div class={`modal-card ${cardClass}`.trim()} role="dialog" aria-modal="true" aria-labelledby={titleId}>
        {#if header}
            {@render header()}
        {:else}
            <h3 id={titleId} class="modal-title">{title}</h3>
            {#if subtitle}
                <p class="modal-subtitle">{subtitle}</p>
            {/if}
        {/if}

        {@render children?.()}

        {#if actions}
            <div class={actionsClass}>
                {@render actions()}
            </div>
        {/if}
    </div>
{/if}
