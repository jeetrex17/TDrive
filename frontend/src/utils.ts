// Pure utility functions for TDrive frontend

export function escapeHtml(input: unknown): string {
    return String(input ?? "")
        .replace(/&/g, "&amp;")
        .replace(/</g, "&lt;")
        .replace(/>/g, "&gt;")
        .replace(/"/g, "&quot;")
        .replace(/'/g, "&#039;");
}

export function splitNameAndExt(filename: string): { base: string; ext: string } {
    const name = typeof filename === "string" ? filename : "";
    const lastDot = name.lastIndexOf(".");
    if (lastDot <= 0 || lastDot === name.length - 1) {
        return { base: name, ext: "FILE" };
    }
    const base = name.slice(0, lastDot);
    const rawExt = name.slice(lastDot + 1);
    const ext = rawExt.replace(/[^a-z0-9]/gi, "").toUpperCase().slice(0, 6) || "FILE";
    return { base, ext };
}

export function formatDate(unixTimestamp: number): string {
    if (!unixTimestamp) return "-";
    const date = new Date(unixTimestamp * 1000);
    return date.toLocaleDateString('en-US', { month: 'short', day: 'numeric', year: 'numeric' });
}

export function formatBytes(bytes: number): string {
    if (bytes === 0) return '0 B';
    const k = 1024;
    const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + ' ' + sizes[i];
}
