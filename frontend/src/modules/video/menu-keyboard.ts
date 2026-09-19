/**
 * Roving-focus keyboard handling and item markup shared by the player's menus.
 *
 * The audio pill, the subtitle pill and the speed menu are three separate
 * popovers that must all answer the arrow keys the same way, because a reader
 * who learns the arrows in one of them will use them in the next. Keeping one
 * copy of that behaviour here is what stops the three from drifting apart -- it
 * is the same reason the checkmark markup lives here rather than being written
 * out again per menu, since a menu whose items are shaped differently cannot be
 * driven by a shared key handler.
 */

/**
 * Escape text destined for a menu item label.
 *
 * Track titles come out of the container's metadata, which is arbitrary bytes
 * from whoever muxed the file, and the menus build their items as HTML strings.
 * Without this a title carrying a `<` truncates the rest of the menu.
 */
export function escapeHTML(value: string): string {
    return value.replace(/[&<>"']/g, (char) => `&#${char.charCodeAt(0)};`);
}

/** One selectable row of a menu: a checkmark the CSS reveals when selected, then the label. */
export function menuItemMarkup(attributes: string, label: string): string {
    return `<button type="button" role="radio" ${attributes} aria-checked="false"><span class="video-menu-check" aria-hidden="true">✓</span><span>${escapeHTML(label)}</span></button>`;
}

/**
 * Move focus within an open menu, and swallow the key so the player's own
 * shortcuts do not also fire.
 *
 * Every handled key calls preventDefault and stopPropagation before returning:
 * ArrowLeft and ArrowRight are seek shortcuts on the player behind the menu, so
 * letting one through would move focus and jump the video ten seconds at once.
 * Unhandled keys return early instead, which is what leaves typing into the
 * custom-speed field working while its menu is open.
 *
 * `buttons` is the caller's list of focusable items, already filtered: a hidden
 * item is still in the DOM and focusing it would strand the focus ring
 * somewhere invisible.
 */
export function handleMenuKeydown(
    event: KeyboardEvent,
    buttons: HTMLButtonElement[],
    close: () => void,
): void {
    if ((event.target as HTMLElement)?.tagName === "INPUT" && event.key === "ArrowDown") {
        buttons[0]?.focus();
        event.preventDefault();
        event.stopPropagation();
        return;
    }
    const current = Math.max(0, buttons.indexOf(document.activeElement as HTMLButtonElement));
    const focusAt = (index: number) => buttons[(index + buttons.length) % buttons.length]?.focus({ preventScroll: true });
    switch (event.key) {
        case "Escape":
            close();
            break;
        case "ArrowDown":
        case "ArrowRight":
            focusAt(current + 1);
            break;
        case "ArrowUp":
        case "ArrowLeft":
            focusAt(current - 1);
            break;
        case "Home":
            focusAt(0);
            break;
        case "End":
            focusAt(buttons.length - 1);
            break;
        case "Enter":
        case " ":
            (document.activeElement as HTMLButtonElement | null)?.click();
            break;
        default:
            return;
    }
    event.preventDefault();
    event.stopPropagation();
}
