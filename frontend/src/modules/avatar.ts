// Avatar view model. Resolves to a profile photo when available, otherwise
// initials on a deterministic accent color derived from the user id. Pure
// data so Svelte components can render it declaratively.

export interface AvatarUser {
    photo_base64?: string;
    display_name?: string;
    username?: string;
    user_id?: number;
}

export interface AvatarView {
    photoUrl?: string;
    initials?: string;
    color?: string;
}

const AVATAR_PALETTE = [
    '#7aa2f7', '#bb9af7', '#7dcfff', '#9ece6a',
    '#e0af68', '#f7768e', '#73daca', '#ff9e64',
];

export function avatarViewFor(user: AvatarUser | null): AvatarView {
    if (!user) return {};

    const photo = String(user.photo_base64 || '').trim();
    if (photo) {
        return { photoUrl: `data:image/jpeg;base64,${photo}` };
    }
    return { initials: initialsFor(user), color: paletteFor(user) };
}

function initialsFor(user: AvatarUser): string {
    const name = String(user.display_name || '').trim();
    if (name && !name.startsWith('@')) {
        const parts = name.split(/\s+/).filter(Boolean);
        if (parts.length >= 2) {
            return (parts[0][0] + parts[parts.length - 1][0]).toUpperCase();
        }
        return parts[0].slice(0, 2).toUpperCase();
    }
    const handle = String(user.username || '').trim();
    if (handle) return handle.slice(0, 2).toUpperCase();
    return '?';
}

function paletteFor(user: AvatarUser): string {
    const id = Number(user.user_id || 0);
    const idx = Math.abs(id) % AVATAR_PALETTE.length;
    return AVATAR_PALETTE[idx];
}
