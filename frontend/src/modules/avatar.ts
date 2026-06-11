// Avatar rendering. Draws a circular profile photo if available, otherwise
// initials on a deterministic accent color derived from the user id.

interface AvatarUser {
    photo_base64?: string;
    display_name?: string;
    username?: string;
    user_id?: number;
}

const AVATAR_PALETTE = [
    '#7aa2f7', '#bb9af7', '#7dcfff', '#9ece6a',
    '#e0af68', '#f7768e', '#73daca', '#ff9e64',
];

export function renderAvatar(el: HTMLElement | null, user: AvatarUser | null): void {
    if (!el) return;
    el.textContent = '';
    el.style.removeProperty('background');
    el.style.removeProperty('background-image');
    el.style.removeProperty('background-size');
    el.style.removeProperty('background-position');

    if (!user) {
        el.style.background = 'var(--surface-2, rgba(255,255,255,0.06))';
        return;
    }

    const photo = String(user.photo_base64 || '').trim();
    if (photo) {
        el.style.backgroundImage = `url("data:image/jpeg;base64,${photo}")`;
        el.style.backgroundSize = 'cover';
        el.style.backgroundPosition = 'center';
        return;
    }

    el.textContent = initialsFor(user);
    el.style.background = paletteFor(user);
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
