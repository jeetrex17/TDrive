import { closeMedia, openStream } from '../../api';
import { formatBytes } from '../../utils';
import { enqueueDownload } from '../transfers';
import { notify } from '../notifications';
import { fileKindLabel, fileOpenKind, type FileOpenKind } from '../media-types';
import FileViewerModal from '../../ui/viewers/FileViewerModal.svelte';
import {
    closeFileViewerView,
    openFileViewerView,
    setFileViewerError,
    setFileViewerLoading,
    type FileViewerKind,
} from '../../ui/viewers/file-viewer-store';
import { mountSvelte, type SvelteMountHandle } from '../../ui/mount';

export interface FileViewerTarget {
    id: number;
    name: string;
    size: number;
    encrypted?: boolean;
}

const STREAM_KINDS = new Set<FileOpenKind>(['audio', 'pdf', 'text']);

let viewerHandle: SvelteMountHandle<Record<string, unknown>> | null = null;
let activeToken = '';
let activeTarget: FileViewerTarget | null = null;
let openSeq = 0;

export function setupFileViewerModal(): void {
    const host = document.getElementById('viewer-modal');
    if (!host || viewerHandle) return;

    host.replaceChildren();
    viewerHandle = mountSvelte(FileViewerModal, {
        target: host,
        props: {
            onClose: closeFileViewer,
            onDownload: downloadActiveFile,
        },
    });
}

export function canOpenFileViewer(name: string): boolean {
    return STREAM_KINDS.has(fileOpenKind(name));
}

export async function openFileViewer(target: FileViewerTarget): Promise<void> {
    const kind = fileOpenKind(target.name);
    if (!STREAM_KINDS.has(kind)) {
        notify({ level: 'warning', title: `${fileKindLabel(target.name)} files cannot be opened yet` });
        return;
    }
    if (target.encrypted) {
        notify({
            level: 'info',
            title: 'Encrypted files open by download for now',
            body: 'Streamed viewers are only enabled for plain files until seekable decrypt is available.',
        });
        enqueueDownload(target.id, target.name, target.size);
        return;
    }

    const seq = ++openSeq;
    await releaseActiveSession();
    activeTarget = {
        id: Number(target.id || 0),
        name: String(target.name || 'File'),
        size: Number(target.size || 0),
        encrypted: Boolean(target.encrypted),
    };

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
        const opened = await openStream(activeTarget.id);
        if (seq !== openSeq) {
            await closeMedia(opened.token);
            return;
        }
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

function downloadActiveFile(): void {
    if (!activeTarget) return;
    enqueueDownload(activeTarget.id, activeTarget.name, activeTarget.size);
}
