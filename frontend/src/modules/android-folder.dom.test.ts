import { afterEach, describe, expect, it, vi } from "vitest";
import { canPickFolder, pickAndroidFolder } from "./android-folder";

interface TestWindow {
    wails?: { pickFolder?: (id: string) => void };
    _wailsAndroidCallback?: ((id: string, result: string | null, error: string | null) => void) | undefined;
    _tdriveFolderCallbacks?: Record<string, unknown>;
}

const host = window as unknown as TestWindow;

/** Stands in for the Android bridge, capturing the id it was handed. */
function installBridge(): { lastId: () => string } {
    let seen = "";
    host.wails = { pickFolder: (id: string) => { seen = id; } };
    return { lastId: () => seen };
}

function answer(id: string, result: string | null, error: string | null = null): void {
    host._wailsAndroidCallback?.(id, result, error);
}

afterEach(() => {
    delete host.wails;
    host._wailsAndroidCallback = undefined;
    delete host._tdriveFolderCallbacks;
});

describe("asking Android for a folder", () => {
    it("reports whether this build can ask at all", () => {
        expect(canPickFolder()).toBe(false);
        installBridge();
        expect(canPickFolder()).toBe(true);
    });

    it("resolves with the path the bridge copied the folder to", async () => {
        const bridge = installBridge();
        const picked = pickAndroidFolder();

        answer(bridge.lastId(), "/data/cache/wails-folder/1/Holiday");
        await expect(picked).resolves.toBe("/data/cache/wails-folder/1/Holiday");
    });

    it("treats a dismissed picker as no folder rather than a failure", async () => {
        // Changing your mind is not an error, and an error here would put a
        // red notice on screen for it.
        const bridge = installBridge();
        const picked = pickAndroidFolder();

        answer(bridge.lastId(), "");
        await expect(picked).resolves.toBe("");
    });

    it("rejects when the bridge could not read the folder", async () => {
        const bridge = installBridge();
        const picked = pickAndroidFolder();

        answer(bridge.lastId(), null, "could not read that folder");
        await expect(picked).rejects.toThrow("could not read that folder");
    });

    it("leaves other host callbacks alone", async () => {
        // The runtime owns this global and every async call into the host comes
        // back through it. Replacing it rather than chaining would break all of
        // them, quietly, for as long as a folder picker had ever been opened.
        const runtime = vi.fn();
        host._wailsAndroidCallback = runtime;
        const bridge = installBridge();
        const picked = pickAndroidFolder();

        answer("some-other-call", "result", null);
        expect(runtime).toHaveBeenCalledWith("some-other-call", "result", null);

        answer(bridge.lastId(), "/tmp/folder");
        await expect(picked).resolves.toBe("/tmp/folder");
        // Ours was handled by us, not passed down.
        expect(runtime).toHaveBeenCalledTimes(1);
    });

    it("chains onto the runtime only once, however many folders are picked", async () => {
        host._wailsAndroidCallback = vi.fn();
        const bridge = installBridge();

        const first = pickAndroidFolder();
        const chained = host._wailsAndroidCallback;
        answer(bridge.lastId(), "/tmp/a");
        await first;

        const second = pickAndroidFolder();
        expect(host._wailsAndroidCallback).toBe(chained);
        answer(bridge.lastId(), "/tmp/b");
        await expect(second).resolves.toBe("/tmp/b");
    });

    it("forgets a picker once it has answered", async () => {
        const bridge = installBridge();
        const picked = pickAndroidFolder();
        answer(bridge.lastId(), "/tmp/folder");
        await picked;

        expect(Object.keys(host._tdriveFolderCallbacks ?? {})).toHaveLength(0);
    });

    it("refuses on a build with no bridge", async () => {
        await expect(pickAndroidFolder()).rejects.toThrow(/cannot open a folder picker/);
    });
});
