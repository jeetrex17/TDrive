<script lang="ts">
    import type { Snippet } from 'svelte';

    interface Props {
        hostId: string;
        children?: Snippet;
    }

    let { hostId, children }: Props = $props();

    function portal(node: HTMLDivElement): { destroy: () => void } | void {
        const host = document.getElementById(hostId);
        if (!host) return;

        host.append(node);
        return {
            destroy() {
                if (node.parentNode === host) node.remove();
            },
        };
    }
</script>

<div style="display: contents" use:portal>
    {@render children?.()}
</div>
