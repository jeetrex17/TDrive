// A small, bounded record of where each view was scrolled, so returning to a
// folder lands back where the reader left it instead of at the top. Pure and
// DOM-free: the file list reads and writes it; the cap keeps it from growing
// with every folder ever visited.

export class ScrollMemory {
    private readonly offsets = new Map<string, number>();

    constructor(private readonly cap: number) {}

    /** Remembers a view's scroll offset, evicting the oldest once over the cap. */
    save(key: string, offset: number): void {
        if (!key) return;
        // Re-insert so the most recently saved view is the newest, and the one
        // evicted is genuinely the least recently touched.
        this.offsets.delete(key);
        this.offsets.set(key, Math.max(0, Math.round(offset)));
        while (this.offsets.size > this.cap) {
            const oldest = this.offsets.keys().next().value;
            if (oldest === undefined) break;
            this.offsets.delete(oldest);
        }
    }

    /** The remembered offset for a view, or undefined when none is held. */
    get(key: string): number | undefined {
        return this.offsets.get(key);
    }

    clear(): void {
        this.offsets.clear();
    }

    get size(): number {
        return this.offsets.size;
    }
}
