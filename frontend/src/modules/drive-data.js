// Thin wrappers over the Wails-generated App bindings for drive data access.
// Guarded so a stale `wails dev` (missing a freshly added binding) fails with
// a clear message instead of an opaque "undefined is not a function".

export async function getFolderContents(parentID) {
    if (window.go?.main?.App?.GetFolderContents) {
        return window.go.main.App.GetFolderContents(parentID);
    }
    throw new Error("GetFolderContents is not available. Restart `wails dev` to regenerate bindings.");
}

export async function deleteFolder(folderID) {
    if (window.go?.main?.App?.DeleteFolder) {
        return window.go.main.App.DeleteFolder(folderID);
    }
    throw new Error("DeleteFolder is not available. Restart `wails dev` to regenerate bindings.");
}

export async function createFolder(name, parentID) {
    if (window.go?.main?.App?.CreateFolder) {
        return window.go.main.App.CreateFolder(name, parentID);
    }
    throw new Error("CreateFolder is not available. Restart `wails dev` to regenerate bindings.");
}

export async function calculateFolderTotalBytes(folderID) {
    const id = String(folderID || "");
    if (!id) return 0;

    if (window.go?.main?.App?.GetFolderSize) {
        const value = await window.go.main.App.GetFolderSize(id);
        const bytes = Number(value);
        return Number.isFinite(bytes) && bytes >= 0 ? bytes : 0;
    }
    throw new Error("GetFolderSize is not available. Restart `wails dev` to regenerate bindings.");
}

export async function getAllFsMsgIDs() {
    if (window.go?.main?.App?.GetAllFsMsgIDs) {
        const ids = await window.go.main.App.GetAllFsMsgIDs();
        return Array.isArray(ids) ? ids : [];
    }
    throw new Error("GetAllFsMsgIDs is not available. Restart `wails dev` to regenerate bindings.");
}
