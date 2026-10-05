<script lang="ts">
    import { channelInitial, type ChannelSource } from './channel-model';

    interface Props {
        source: ChannelSource;
        /** Resolves to a data URL, or '' when the channel has no photo. */
        loadPhoto: (source: ChannelSource) => Promise<string>;
    }

    let { source, loadPhoto }: Props = $props();
    let photo = $state('');

    // Asks for the picture once the avatar is on screen, so a long picker
    // list fetches only the ones someone can see. The initial shows until
    // then, and for good when the channel has no photo.
    function whenVisible(node: HTMLElement) {
        const load = () => void loadPhoto(source).then((url) => { photo = url; });
        if (typeof IntersectionObserver === 'undefined') {
            load();
            return;
        }
        const observer = new IntersectionObserver((entries) => {
            if (!entries.some((entry) => entry.isIntersecting)) return;
            observer.disconnect();
            load();
        });
        observer.observe(node);
        return { destroy: () => observer.disconnect() };
    }
</script>

<!-- Sized by its row through --avatar-size, which inherits. -->
<span class="channel-avatar" use:whenVisible aria-hidden="true">
    {channelInitial(source.title)}
    {#if photo}<img src={photo} alt="" decoding="async" />{/if}
</span>

<style>
    .channel-avatar {
        position: relative;
        flex: 0 0 auto;
        width: var(--avatar-size, 24px);
        height: var(--avatar-size, 24px);
        display: grid;
        place-items: center;
        overflow: hidden;
        border-radius: var(--radius-pill);
        background: var(--overlay-neutral-3);
        color: var(--text-main);
        font-size: calc(var(--avatar-size, 24px) * 0.42);
        font-weight: var(--weight-strong);
        line-height: 1;
    }

    .channel-avatar img {
        position: absolute;
        inset: 0;
        width: 100%;
        height: 100%;
        object-fit: cover;
        animation: channel-avatar-in var(--motion-med) var(--ease-standard);
    }

    @keyframes channel-avatar-in {
        from { opacity: 0; }
    }

    @media (prefers-reduced-motion: reduce) {
        .channel-avatar img { animation: none; }
    }
</style>
