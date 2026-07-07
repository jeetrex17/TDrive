<script lang="ts">
    import { avatarViewFor, type AvatarUser } from '../../modules/avatar';

    interface Props {
        user: AvatarUser | null;
        large?: boolean;
    }

    let { user, large = false }: Props = $props();

    const view = $derived(avatarViewFor(user));
    const style = $derived(
        view.photoUrl
            ? `background-image: url("${view.photoUrl}"); background-size: cover; background-position: center;`
            : view.color
                ? `background: ${view.color};`
                : 'background: var(--surface-2, rgba(255,255,255,0.06));',
    );
</script>

<span class={`profile-avatar${large ? ' profile-avatar-lg' : ''}`} aria-hidden="true" {style}>{view.initials ?? ''}</span>
