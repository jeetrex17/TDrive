// Svelte action that reparents an element to document.body. The bell's
// popover and panel are positioned with fixed viewport coordinates; hosting
// them under the header would subject them to whatever positioned ancestor
// or overflow clipping the header chrome happens to have.
export function portal(node: HTMLElement): { destroy(): void } {
    document.body.appendChild(node);
    return {
        destroy() {
            node.remove();
        },
    };
}
