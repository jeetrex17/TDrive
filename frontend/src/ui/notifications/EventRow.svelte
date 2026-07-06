<script lang="ts">
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
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><polyline points="20 6 9 17 4 12"/></svg>
        {:else if event.level === 'error'}
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><line x1="15" y1="9" x2="9" y2="15"/><line x1="9" y1="9" x2="15" y2="15"/></svg>
        {:else if event.level === 'warning'}
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z"/><line x1="12" y1="9" x2="12" y2="13"/><line x1="12" y1="17" x2="12.01" y2="17"/></svg>
        {:else}
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><line x1="12" y1="16" x2="12" y2="12"/><line x1="12" y1="8" x2="12.01" y2="8"/></svg>
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
