<script lang="ts">
    import CheckIcon from '@lucide/svelte/icons/check';
    import CircleXIcon from '@lucide/svelte/icons/circle-x';
    import InfoIcon from '@lucide/svelte/icons/info';
    import TriangleAlertIcon from '@lucide/svelte/icons/triangle-alert';
    import type { NoticeEvent } from './notif-store';

    interface Props {
        event: NoticeEvent;
    }

    let { event }: Props = $props();
    let copied = $state(false);
    let copiedTimer: ReturnType<typeof setTimeout> | null = null;

    const clipable = $derived(event.level === 'error' && Boolean(event.body));

    function formatRelative(ts: number): string {
        if (!ts) return '';
        const diffSec = Math.floor((Date.now() - ts) / 1000);
        if (diffSec < 30) return 'just now';
        if (diffSec < 60) return `${diffSec}s ago`;
        const m = Math.floor(diffSec / 60);
        if (m < 60) return `${m} min ago`;
        const h = Math.floor(m / 60);
        if (h < 24) return `${h} hr ago`;
        const d = Math.floor(h / 24);
        if (d < 7) return `${d}d ago`;
        return new Date(ts).toLocaleDateString();
    }

    async function copyBody(): Promise<void> {
        if (!clipable) return;
        try {
            await navigator.clipboard.writeText(`${event.title}\n${event.body}`);
        } catch {
            return;
        }
        copied = true;
        if (copiedTimer) clearTimeout(copiedTimer);
        copiedTimer = setTimeout(() => {
            copied = false;
            copiedTimer = null;
        }, 800);
    }
</script>

<!-- svelte-ignore a11y_no_static_element_interactions, a11y_click_events_have_key_events -->
<div
    class={`notif-row notif-row-event level-${event.level}${copied ? ' notif-copied' : ''}`}
    data-clipable={clipable ? '1' : '0'}
    onclick={() => void copyBody()}
>
    <span class="notif-row-icon" data-kind={event.level} aria-hidden="true">
        {#if event.level === 'success'}
            <CheckIcon size={14} strokeWidth={2.2} aria-hidden="true" />
        {:else if event.level === 'error'}
            <CircleXIcon size={14} strokeWidth={2} aria-hidden="true" />
        {:else if event.level === 'warning'}
            <TriangleAlertIcon size={14} strokeWidth={2} aria-hidden="true" />
        {:else}
            <InfoIcon size={14} strokeWidth={2} aria-hidden="true" />
        {/if}
    </span>
    <div class="notif-row-body">
        <div class="notif-row-title">{event.title}</div>
        {#if event.body}
            <div class="notif-row-sub">{event.body}</div>
        {/if}
    </div>
    <div class="notif-row-meta">{formatRelative(event.ts)}</div>
</div>
