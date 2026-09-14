import { closeMedia, onRuntimeEvent, openStream } from '../../api';
import { formatBytes } from '../../utils';
import { enqueueDownload } from '../transfers';
import { notify } from '../notifications';
import { canOpenFileViewer, fileKindLabel, fileOpenKind } from '../media-types';
import {
    closeFileViewerView,
    openFileViewerView,
    setFileViewerError,
    setFileViewerLoading,
    type FileViewerKind,
} from '../../ui/viewers/file-viewer-store';

export interface FileViewerTarget {
    id: number;
    name: string;
    size: number;
    encrypted?: boolean;
}



let viewerHost: HTMLElement | null = null;
let lifecycleObserver: MutationObserver | null = null;
let activeToken = '';
let activeTarget: FileViewerTarget | null = null;
let openSeq = 0;
let encryptedSessionsEpoch = 0;
let unsubscribeEncryptedSessionsClosed: (() => void) | null = null;

function bindEncryptedMediaLifecycle(): void {
    if (unsubscribeEncryptedSessionsClosed) return;
    unsubscribeEncryptedSessionsClosed = onRuntimeEvent('encrypted_media_sessions_closed', () => {
        encryptedSessionsEpoch += 1;
        if (activeTarget?.encrypted) closeFileViewer();
    });
}

export function activateFileViewerModal(): () => void {
    const host = document.getElementById('viewer-modal');
    if (!host) return () => {};
    if (viewerHost === host) return teardownFileViewerModal;

    teardownFileViewerModal();
    viewerHost = host;
    bindEncryptedMediaLifecycle();
    lifecycleObserver = new MutationObserver(() => {
        if (!host.isConnected) teardownFileViewerModal();
    });
    lifecycleObserver.observe(document.body, { childList: true, subtree: true });
    return teardownFileViewerModal;
}

export function teardownFileViewerModal(): void {
    lifecycleObserver?.disconnect();
    lifecycleObserver = null;
    viewerHost = null;
    unsubscribeEncryptedSessionsClosed?.();
    unsubscribeEncryptedSessionsClosed = null;
    closeFileViewer();
}



export async function openFileViewer(target: FileViewerTarget): Promise<void> {
    const kind = fileOpenKind(target.name);
    if (!canOpenFileViewer(target.name)) {
        notify({ level: 'warning', title: `${fileKindLabel(target.name)} files cannot be opened yet` });
        return;
    }
    const seq = ++openSeq;
    // The backend can reveal encryption only after a pending open completes.
    const encryptedEpoch = encryptedSessionsEpoch;
    const nextTarget = {
        id: Number(target.id || 0),
        name: String(target.name || 'File'),
        size: Number(target.size || 0),
        encrypted: Boolean(target.encrypted),
    };
    activeTarget = nextTarget;
    closeFileViewerView();
    await releaseActiveSession();
    if (seq !== openSeq) return;

    openFileViewerView({
        kind: kind as FileViewerKind,
        token: '',
        url: '',
        title: activeTarget.name,
        meta: `${fileKindLabel(activeTarget.name)} · ${formatBytes(activeTarget.size)}`,
        mimeType: '',
        loading: true,
        error: '',
    });

    try {
        const opened = await openStream(nextTarget.id);
        if (seq !== openSeq) {
            await closeMedia(opened.token);
            return;
        }
        const encrypted = nextTarget.encrypted || Boolean(opened.info.encrypted);
        if (encrypted && encryptedEpoch !== encryptedSessionsEpoch) {
            closeFileViewer();
            await closeMedia(opened.token);
            return;
        }
        activeTarget = { ...nextTarget, encrypted };
        activeToken = opened.token;
        openFileViewerView({
            kind: kind as FileViewerKind,
            token: opened.token,
            url: opened.url,
            title: opened.name || activeTarget.name,
            meta: `${fileKindLabel(opened.name || activeTarget.name)} · ${formatBytes(opened.info.plaintextSize || opened.info.storedSize || activeTarget.size)}`,
            mimeType: opened.mimeType,
            loading: false,
            error: '',
        });
    } catch (error) {
        if (seq !== openSeq) return;
        setFileViewerError(String(error || 'Could not open file'));
    } finally {
        if (seq === openSeq) setFileViewerLoading(false);
    }
}

async function releaseActiveSession(): Promise<void> {
    const token = activeToken;
    activeToken = '';
    if (!token) return;
    try {
        await closeMedia(token);
    } catch {
        // Closing a stale loopback token is best-effort; the backend also
        // releases sessions on app shutdown.
    }
}

export function closeFileViewer(): void {
    openSeq += 1;
    void releaseActiveSession();
    activeTarget = null;
    closeFileViewerView();
}

export function downloadActiveFile(): void {
    if (!activeTarget) return;
    enqueueDownload(activeTarget.id, activeTarget.name, activeTarget.size);
}
