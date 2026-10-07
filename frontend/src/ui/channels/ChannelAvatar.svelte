<script lang="ts">
    import BotIcon from '@lucide/svelte/icons/bot';
    import RadioTowerIcon from '@lucide/svelte/icons/radio-tower';
    import UserRoundIcon from '@lucide/svelte/icons/user-round';
    import UsersRoundIcon from '@lucide/svelte/icons/users-round';
    import { channelInitial, sourcePeerLabel, type ChannelSource } from './channel-model';

    interface Props {
        source: ChannelSource;
        /** Resolves to a data URL, or '' when the channel has no photo. */
        loadPhoto: (source: ChannelSource) => Promise<string>;
    }

    let { source, loadPhoto }: Props = $props();
    let photo = $state('');
    const PeerIcon = $derived(source.peerKind === 'bot' ? BotIcon
        : source.peerKind === 'channel' ? RadioTowerIcon
            : source.peerKind === 'group' || source.peerKind === 'supergroup' ? UsersRoundIcon
                : UserRoundIcon);

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
<span class="channel-avatar" use:whenVisible aria-label={sourcePeerLabel(source)} title={sourcePeerLabel(source)}>
    <span class="channel-avatar-initial" aria-hidden="true">{channelInitial(source.title)}</span>
    <span class="channel-avatar-peer" aria-hidden="true"><PeerIcon size={Math.max(11, Math.round(16 * 0.7))} strokeWidth={2} /></span>
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

    .channel-avatar-peer {
        position: absolute;
        right: -1px;
        bottom: -1px;
        display: grid;
        width: calc(var(--avatar-size, 24px) * 0.46);
        height: calc(var(--avatar-size, 24px) * 0.46);
        min-width: 11px;
        min-height: 11px;
        place-items: center;
        border: 1px solid var(--color-surface-0);
        border-radius: var(--radius-pill);
        background: var(--color-surface-2);
        color: var(--text-muted);
    }

    @keyframes channel-avatar-in {
        from { opacity: 0; }
    }

    @media (prefers-reduced-motion: reduce) {
        .channel-avatar img { animation: none; }
    }
</style>
