<script lang="ts">
    import { onDestroy, untrack } from 'svelte';
    import ModalShell from '../modals/ModalShell.svelte';
    import { fileViewerState } from './file-viewer-store';

    interface Props {
        onClose: () => void;
        onDownload: () => void;
    }

    let { onClose, onDownload }: Props = $props();

    const TEXT_CHUNK_BYTES = 512 * 1024;
    const TEXT_MAX_BYTES = 5 * 1024 * 1024;

    let audioEl = $state<HTMLAudioElement | null>(null);
    let audioCurrent = $state(0);
    let audioDuration = $state(0);
    let audioPaused = $state(true);
    let audioWaiting = $state(false);
    let audioVolume = $state(1);
    let activeAudioUrl = '';

    let pdfCanvas = $state<HTMLCanvasElement | null>(null);
    let pdfDoc = $state<any>(null);
    let pdfPageNumber = $state(1);
    let pdfPageCount = $state(0);
    let pdfScale = $state(1.15);
    let pdfLoading = $state(false);
    let pdfRendering = $state(false);
    let pdfError = $state('');
    let pdfLoadGeneration = 0;
    let pdfRenderGeneration = 0;
    let activePdfUrl = '';

    let textContent = $state('');
    let textOffset = $state(0);
    let textTotal = $state(0);
    let textLoading = $state(false);
    let textError = $state('');
    let textDone = $state(false);
    let textDecoder = new TextDecoder('utf-8', { fatal: false });
    let textGeneration = 0;

    function formatTime(seconds: number): string {
        if (!Number.isFinite(seconds) || seconds <= 0) return '0:00';
        const whole = Math.floor(seconds);
        const h = Math.floor(whole / 3600);
        const m = Math.floor((whole % 3600) / 60);
        const s = whole % 60;
        if (h > 0) return `${h}:${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`;
        return `${m}:${String(s).padStart(2, '0')}`;
    }

    function viewerLabel(): string {
        switch ($fileViewerState.kind) {
            case 'audio':
                return 'Audio';
            case 'pdf':
                return 'PDF';
            case 'text':
                return 'Text';
            default:
                return 'File';
        }
    }

    function viewerStatus(): string {
        if ($fileViewerState.loading) return 'Opening';
        if ($fileViewerState.error) return 'Unavailable';
        if ($fileViewerState.kind === 'audio') return audioWaiting ? 'Buffering' : audioPaused ? 'Ready' : 'Playing';
        if ($fileViewerState.kind === 'pdf') return pdfLoading ? 'Opening PDF' : pdfRendering ? 'Rendering' : pdfPageCount ? `${pdfPageCount} pages` : 'Ready';
        if ($fileViewerState.kind === 'text') return textLoading && !textOffset ? 'Opening text' : textDone ? 'Loaded' : textOffset ? 'Partial' : 'Ready';
        return 'Ready';
    }

    function syncAudio(): void {
        if (!audioEl) return;
        audioCurrent = Number.isFinite(audioEl.currentTime) ? audioEl.currentTime : 0;
        audioDuration = Number.isFinite(audioEl.duration) ? audioEl.duration : 0;
        audioPaused = audioEl.paused;
        audioWaiting = audioEl.readyState < HTMLMediaElement.HAVE_FUTURE_DATA && !audioEl.paused;
        audioVolume = audioEl.volume;
    }

    function resetAudioState(): void {
        audioCurrent = 0;
        audioDuration = 0;
        audioPaused = true;
        audioWaiting = false;
        audioVolume = 1;
    }

    async function toggleAudio(): Promise<void> {
        if (!audioEl) return;
        if (audioEl.paused) {
            await audioEl.play();
        } else {
            audioEl.pause();
        }
        syncAudio();
    }

    function seekAudio(event: Event): void {
        if (!audioEl) return;
        const value = Number((event.currentTarget as HTMLInputElement).value);
        if (!Number.isFinite(value)) return;
        audioEl.currentTime = value;
        audioCurrent = value;
    }

    function setAudioVolume(event: Event): void {
        if (!audioEl) return;
        const value = Number((event.currentTarget as HTMLInputElement).value);
        if (!Number.isFinite(value)) return;
        audioEl.volume = Math.max(0, Math.min(1, value));
        audioEl.muted = audioEl.volume <= 0;
        syncAudio();
    }

    function toggleAudioMute(): void {
        if (!audioEl) return;
        audioEl.muted = !audioEl.muted;
        syncAudio();
    }

    async function loadPdfJs(): Promise<any> {
        const [pdfjs, worker] = await Promise.all([
            import('pdfjs-dist/legacy/build/pdf.mjs'),
            import('pdfjs-dist/legacy/build/pdf.worker.mjs?url'),
        ]);
        pdfjs.GlobalWorkerOptions.workerSrc = worker.default;
        return pdfjs;
    }

    async function loadPdf(url: string, generation: number): Promise<void> {
        pdfLoading = true;
        pdfError = '';
        pdfPageNumber = 1;
        pdfPageCount = 0;
        try {
            const pdfjs = await loadPdfJs();
            const loadingTask = pdfjs.getDocument({
                url,
                disableAutoFetch: false,
                disableStream: false,
                rangeChunkSize: 1024 * 1024,
            });
            const doc = await loadingTask.promise;
            if (generation !== pdfLoadGeneration) {
                await doc.destroy?.();
                return;
            }
            pdfDoc = doc;
            pdfPageCount = Number(doc.numPages || 0);
        } catch (error) {
            if (generation === pdfLoadGeneration) {
                pdfError = String(error || 'Could not open PDF');
            }
        } finally {
            if (generation === pdfLoadGeneration) pdfLoading = false;
        }
    }

    function destroyPdfDocument(): void {
        const doc = untrack(() => pdfDoc);
        doc?.destroy?.();
        pdfDoc = null;
    }

    function resetPdfState(): void {
        destroyPdfDocument();
        pdfPageCount = 0;
        pdfPageNumber = 1;
        pdfError = '';
        pdfLoading = false;
        pdfRendering = false;
    }

    async function renderPdfPage(doc: any, pageNumber: number, scale: number, generation: number): Promise<void> {
        if (!pdfCanvas) return;
        pdfRendering = true;
        try {
            const page = await doc.getPage(pageNumber);
            if (generation !== pdfRenderGeneration || !pdfCanvas) return;
            const viewport = page.getViewport({ scale });
            const ratio = window.devicePixelRatio || 1;
            const context = pdfCanvas.getContext('2d', { alpha: false });
            if (!context) throw new Error('Canvas is not available');
            pdfCanvas.width = Math.ceil(viewport.width * ratio);
            pdfCanvas.height = Math.ceil(viewport.height * ratio);
            pdfCanvas.style.width = `${Math.ceil(viewport.width)}px`;
            pdfCanvas.style.height = `${Math.ceil(viewport.height)}px`;
            context.setTransform(ratio, 0, 0, ratio, 0, 0);
            await page.render({ canvasContext: context, viewport }).promise;
        } catch (error) {
            if (generation === pdfRenderGeneration) pdfError = String(error || 'Could not render PDF page');
        } finally {
            if (generation === pdfRenderGeneration) pdfRendering = false;
        }
    }

    function setPdfPage(page: number): void {
        if (!pdfDoc) return;
        pdfPageNumber = Math.max(1, Math.min(pdfPageCount || 1, page));
    }

    function setPdfZoom(nextScale: number): void {
        pdfScale = Math.max(0.6, Math.min(2.4, nextScale));
    }

    function parseContentRangeTotal(header: string | null): number {
        if (!header) return 0;
        const match = /\/(\d+)$/.exec(header.trim());
        return match ? Number(match[1]) : 0;
    }

    async function loadTextChunk(reset = false): Promise<void> {
        const url = $fileViewerState.url;
        if (!$fileViewerState.open || $fileViewerState.kind !== 'text' || !url) return;
        if (textLoading || textDone && !reset) return;

        const generation = textGeneration;
        const offset = reset ? 0 : textOffset;
        const end = Math.min(offset + TEXT_CHUNK_BYTES, TEXT_MAX_BYTES) - 1;
        textLoading = true;
        textError = '';
        try {
            const response = await fetch(url, {
                headers: { Range: `bytes=${offset}-${end}` },
            });
            if (!response.ok && response.status !== 206) {
                throw new Error(`Text request failed (${response.status})`);
            }
            const buffer = await response.arrayBuffer();
            if (generation !== textGeneration) return;

            const total = parseContentRangeTotal(response.headers.get('Content-Range'));
            const nextOffset = offset + buffer.byteLength;
            const reachedFileEnd = total > 0 && nextOffset >= total;
            const reachedViewerCap = nextOffset >= TEXT_MAX_BYTES;
            const chunk = textDecoder.decode(buffer, { stream: !reachedFileEnd && !reachedViewerCap });

            textContent = reset ? chunk : textContent + chunk;
            textOffset = nextOffset;
            textTotal = total;
            textDone = buffer.byteLength === 0 || reachedFileEnd || reachedViewerCap;
        } catch (error) {
            if (generation === textGeneration) textError = String(error || 'Could not open text file');
        } finally {
            if (generation === textGeneration) textLoading = false;
        }
    }

    function resetText(): void {
        textGeneration += 1;
        textDecoder = new TextDecoder('utf-8', { fatal: false });
        textContent = '';
        textOffset = 0;
        textTotal = 0;
        textLoading = false;
        textError = '';
        textDone = false;
    }

    $effect(() => {
        const { open, kind, url } = $fileViewerState;
        const nextUrl = open && kind === 'audio' ? url : '';
        if (nextUrl === activeAudioUrl) return;
        activeAudioUrl = nextUrl;
        resetAudioState();
    });

    $effect(() => {
        const { open, kind, url } = $fileViewerState;
        const nextUrl = open && kind === 'pdf' ? url : '';

        if (nextUrl === activePdfUrl) {
            return;
        }

        activePdfUrl = nextUrl;
        pdfLoadGeneration += 1;
        resetPdfState();

        if (!nextUrl) {
            return;
        }

        const generation = pdfLoadGeneration;
        void loadPdf(nextUrl, generation);
    });

    $effect(() => {
        if (!$fileViewerState.open || $fileViewerState.kind !== 'pdf' || !pdfDoc || !pdfCanvas) return;
        pdfRenderGeneration += 1;
        const generation = pdfRenderGeneration;
        void renderPdfPage(pdfDoc, pdfPageNumber, pdfScale, generation);
    });

    $effect(() => {
        const { open, kind, url } = $fileViewerState;
        resetText();
        if (open && kind === 'text' && url) {
            void loadTextChunk(true);
        }
    });

    onDestroy(() => {
        pdfLoadGeneration += 1;
        destroyPdfDocument();
    });
</script>

{#snippet viewerHeader()}
    <div class="file-viewer-topbar">
        <div class="file-viewer-identity">
            <div class={`file-viewer-kind-mark is-${$fileViewerState.kind || 'file'}`} aria-hidden="true">
                {#if $fileViewerState.kind === 'audio'}
                    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9">
                        <path stroke-linecap="round" stroke-linejoin="round" d="M9 18V5l11-2v13" />
                        <circle cx="6" cy="18" r="3" />
                        <circle cx="17" cy="16" r="3" />
                    </svg>
                {:else if $fileViewerState.kind === 'pdf'}
                    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8">
                        <path stroke-linejoin="round" d="M7 3h7l4 4v14H7z" />
                        <path stroke-linecap="round" d="M14 3v5h5M9.5 13h5M9.5 16h3.5" />
                    </svg>
                {:else if $fileViewerState.kind === 'text'}
                    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8">
                        <path stroke-linejoin="round" d="M7 3h10v18H7z" />
                        <path stroke-linecap="round" d="M10 8h4M10 12h4M10 16h3" />
                    </svg>
                {:else}
                    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8">
                        <path stroke-linejoin="round" d="M7 3h7l4 4v14H7z" />
                        <path stroke-linecap="round" d="M14 3v5h5" />
                    </svg>
                {/if}
            </div>
            <div class="file-viewer-title-group">
                <h3 id="file-viewer-title" class="file-viewer-title" title={$fileViewerState.title}>
                    {$fileViewerState.title || 'Open file'}
                </h3>
                <div class="file-viewer-meta">
                    <span>{viewerLabel()}</span>
                    {#if $fileViewerState.meta}
                        <span aria-hidden="true">·</span>
                        <span>{$fileViewerState.meta}</span>
                    {/if}
                    <span aria-hidden="true">·</span>
                    <span>{viewerStatus()}</span>
                </div>
            </div>
        </div>
        <div class="file-viewer-actions">
            <button class="file-viewer-action" type="button" onclick={onDownload} aria-label="Download" title="Download">
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M12 3v12m0 0l-4-4m4 4l4-4M5 21h14" />
                </svg>
            </button>
            <button class="file-viewer-close" type="button" onclick={onClose} aria-label="Close file" title="Close">
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" aria-hidden="true">
                    <path stroke-linecap="round" d="M6 6l12 12M18 6L6 18" />
                </svg>
            </button>
        </div>
    </div>
{/snippet}

<ModalShell
    hostId="viewer-modal"
    open={$fileViewerState.open}
    titleId="file-viewer-title"
    cardClass={`file-viewer-shell ${$fileViewerState.kind ? `is-${$fileViewerState.kind}` : ''}`}
    initialFocus=".file-viewer-close"
    restoreFocus="#file-list"
    header={viewerHeader}
    onClose={onClose}
>
    <div class="file-viewer-stage">
        {#if $fileViewerState.error}
            <div class="file-viewer-state" role="alert">{$fileViewerState.error}</div>
        {:else if $fileViewerState.kind === 'audio'}
            <div class="audio-viewer">
                <audio
                    bind:this={audioEl}
                    src={$fileViewerState.url}
                    preload="metadata"
                    onloadedmetadata={syncAudio}
                    ontimeupdate={syncAudio}
                    onplay={syncAudio}
                    onpause={syncAudio}
                    onwaiting={() => { audioWaiting = true; }}
                    oncanplay={syncAudio}
                    onended={syncAudio}
                    onerror={() => { audioWaiting = false; }}
                ></audio>
                <div class="audio-hero">
                    <div class="audio-art" aria-hidden="true">
                        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8">
                            <path stroke-linecap="round" stroke-linejoin="round" d="M9 18V5l11-2v13" />
                            <circle cx="6" cy="18" r="3" />
                            <circle cx="17" cy="16" r="3" />
                        </svg>
                    </div>
                    <div class="audio-copy">
                        <div class="audio-title" title={$fileViewerState.title}>{$fileViewerState.title}</div>
                        <div class="audio-subtitle">{audioWaiting ? 'Buffering from Telegram' : audioPaused ? 'Ready to play' : 'Streaming audio'}</div>
                    </div>
                </div>
                <div class="audio-timeline">
                    <span class="audio-time">{formatTime(audioCurrent)}</span>
                    <input class="audio-seek" type="range" min="0" max={audioDuration || 0} step="0.1" value={audioCurrent} oninput={seekAudio} aria-label="Seek audio" />
                    <span class="audio-time">{formatTime(audioDuration)}</span>
                </div>
                <div class="audio-controls" aria-label="Audio controls">
                    <button class="audio-play" type="button" onclick={() => void toggleAudio()} aria-label={audioPaused ? 'Play audio' : 'Pause audio'}>
                        {#if audioPaused}
                            <svg viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d="M8 5.8c0-.9.98-1.45 1.74-.98l9.08 5.67a1.15 1.15 0 010 1.96l-9.08 5.67A1.15 1.15 0 018 17.14V5.8z" /></svg>
                        {:else}
                            <svg viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d="M7 5h3.4v14H7V5zm6.6 0H17v14h-3.4V5z" /></svg>
                        {/if}
                    </button>
                    <div class="audio-volume-group">
                        <button class="audio-mute" type="button" onclick={toggleAudioMute} aria-label={audioEl?.muted ? 'Unmute' : 'Mute'}>
                            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                                <path stroke-linecap="round" stroke-linejoin="round" d="M4 10v4h4l5 4V6l-5 4H4z" />
                                {#if audioEl?.muted || audioVolume <= 0}
                                    <path stroke-linecap="round" stroke-linejoin="round" d="M18 9l-5 6m0-6l5 6" />
                                {:else}
                                    <path stroke-linecap="round" stroke-linejoin="round" d="M16 9.5a4 4 0 010 5" />
                                {/if}
                            </svg>
                        </button>
                        <input class="audio-volume" type="range" min="0" max="1" step="0.01" value={audioVolume} oninput={setAudioVolume} aria-label="Volume" />
                    </div>
                </div>
            </div>
        {:else if $fileViewerState.kind === 'pdf'}
            <div class="pdf-viewer">
                <div class="pdf-toolbar" aria-label="PDF controls">
                    <button class="file-viewer-action is-icon-only" type="button" disabled={!pdfDoc || pdfPageNumber <= 1} onclick={() => setPdfPage(pdfPageNumber - 1)} aria-label="Previous page" title="Previous page">
                        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                            <path stroke-linecap="round" stroke-linejoin="round" d="M15 18l-6-6 6-6" />
                        </svg>
                    </button>
                    <span class="pdf-page-label">{pdfPageCount ? `Page ${pdfPageNumber} / ${pdfPageCount}` : 'Loading PDF'}</span>
                    <button class="file-viewer-action is-icon-only" type="button" disabled={!pdfDoc || pdfPageNumber >= pdfPageCount} onclick={() => setPdfPage(pdfPageNumber + 1)} aria-label="Next page" title="Next page">
                        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                            <path stroke-linecap="round" stroke-linejoin="round" d="M9 18l6-6-6-6" />
                        </svg>
                    </button>
                    <span class="pdf-toolbar-spacer"></span>
                    <button class="file-viewer-action is-icon-only" type="button" onclick={() => setPdfZoom(pdfScale - 0.15)} aria-label="Zoom out" title="Zoom out">−</button>
                    <span class="pdf-page-label">{Math.round(pdfScale * 100)}%</span>
                    <button class="file-viewer-action is-icon-only" type="button" onclick={() => setPdfZoom(pdfScale + 0.15)} aria-label="Zoom in" title="Zoom in">+</button>
                </div>
                <div class="pdf-stage">
                    {#if pdfError}
                        <div class="file-viewer-state" role="alert">{pdfError}</div>
                    {:else if pdfLoading}
                        <div class="file-viewer-state">Opening PDF…</div>
                    {/if}
                    <canvas bind:this={pdfCanvas} class:is-rendering={pdfRendering}></canvas>
                </div>
            </div>
        {:else if $fileViewerState.kind === 'text'}
            <div class="text-viewer">
                {#if textError}
                    <div class="file-viewer-state" role="alert">{textError}</div>
                {/if}
                <div class="text-viewer-scroll">
                    <pre class="text-viewer-body">{textContent}</pre>
                </div>
                <div class="text-viewer-footer">
                    <span>{textOffset ? `${(textOffset / 1024).toFixed(0)} KB loaded` : textLoading ? 'Opening text…' : ''}</span>
                    {#if textTotal}
                        <span>{Math.round((textOffset / textTotal) * 100)}%</span>
                    {/if}
                    <button class="file-viewer-action" type="button" disabled={textLoading || textDone} onclick={() => void loadTextChunk()}>
                        {textDone ? 'End of file' : textLoading ? 'Loading…' : 'Load more'}
                    </button>
                </div>
            </div>
        {:else}
            <div class="file-viewer-state">Opening file…</div>
        {/if}
    </div>
</ModalShell>
