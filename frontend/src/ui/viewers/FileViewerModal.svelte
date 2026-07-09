<script lang="ts">
    import ModalShell from '../modals/ModalShell.svelte';
    import { isPdfFrameMessage, pdfViewerFrameSrc } from './pdf-frame';
    import { fileViewerState } from './file-viewer-store';
    import {
        structuredTextLanguageForName,
        textViewerModeForName,
        type TextViewerMode,
    } from './text-viewer-types';

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
    let audioError = $state('');
    let activeAudioUrl = '';

    let pdfFrameEl = $state<HTMLIFrameElement | null>(null);
    let pdfLoading = $state(false);
    let pdfError = $state('');
    let pdfPageCount = $state(0);
    let activePdfUrl = '';

    let textContent = $state('');
    let textOffset = $state(0);
    let textTotal = $state(0);
    let textLoading = $state(false);
    let textError = $state('');
    let textDone = $state(false);
    let textRenderedHtml = $state('');
    let textDecoder = new TextDecoder('utf-8', { fatal: false });
    let textGeneration = 0;
    let activeTextSource = '';
    let markdownView = $state<'rendered' | 'raw'>('rendered');

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
        if ($fileViewerState.kind === 'audio') return audioError ? 'Unavailable' : audioWaiting ? 'Buffering' : audioPaused ? 'Ready' : 'Playing';
        if ($fileViewerState.kind === 'pdf') return pdfError ? 'Unavailable' : pdfLoading ? 'Opening PDF' : pdfPageCount ? `${pdfPageCount} pages` : 'Ready';
        if ($fileViewerState.kind === 'text') {
            const mode = currentTextMode();
            if (textLoading && !textOffset) return mode === 'plain' ? 'Opening text' : 'Rendering preview';
            if (mode === 'plain') return textDone ? textCompletionLabel() : textOffset ? 'Partial' : 'Ready';
            return textDone && textOffset && textTotal > 0 && textOffset >= textTotal ? 'Preview ready' : textOffset ? 'Preview ready' : 'Ready';
        }
        return 'Ready';
    }

    function syncAudio(): void {
        if (!audioEl) return;
        audioCurrent = Number.isFinite(audioEl.currentTime) ? audioEl.currentTime : 0;
        audioDuration = Number.isFinite(audioEl.duration) ? audioEl.duration : 0;
        audioPaused = audioEl.paused;
        audioWaiting = audioEl.readyState < HTMLMediaElement.HAVE_FUTURE_DATA && !audioEl.paused;
        audioVolume = audioEl.volume;
        if (audioEl.readyState >= HTMLMediaElement.HAVE_METADATA) audioError = '';
    }

    function resetAudioState(): void {
        audioCurrent = 0;
        audioDuration = 0;
        audioPaused = true;
        audioWaiting = false;
        audioVolume = 1;
        audioError = '';
    }

    async function toggleAudio(): Promise<void> {
        if (!audioEl) return;
        if (audioEl.paused) {
            try {
                await audioEl.play();
                audioError = '';
            } catch (error) {
                audioError = String(error || 'Could not play audio');
            }
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

    function parseContentRangeTotal(header: string | null): number {
        if (!header) return 0;
        const match = /\/(\d+)$/.exec(header.trim());
        return match ? Number(match[1]) : 0;
    }

    function isTextResponseOK(response: Response): boolean {
        return response.ok && (response.status === 200 || response.status === 206);
    }

    async function loadTextChunk(reset = false): Promise<void> {
        const url = $fileViewerState.url;
        if (!$fileViewerState.open || $fileViewerState.kind !== 'text' || !url || currentTextMode() !== 'plain') return;
        if (textLoading || textDone && !reset) return;

        const generation = textGeneration;
        const offset = reset ? 0 : textOffset;
        const end = Math.min(offset + TEXT_CHUNK_BYTES, TEXT_MAX_BYTES) - 1;
        const requestedBytes = Math.max(0, end - offset + 1);
        textLoading = true;
        textError = '';
        try {
            const response = await fetch(url, {
                headers: { Range: `bytes=${offset}-${end}` },
            });
            if (!isTextResponseOK(response)) {
                throw new Error(`Text request failed (${response.status})`);
            }
            const buffer = await response.arrayBuffer();
            if (generation !== textGeneration) return;

            const isPartial = response.status === 206;
            const total = parseContentRangeTotal(response.headers.get('Content-Range'));
            const nextOffset = offset + buffer.byteLength;
            const reachedFileEnd = total > 0
                ? nextOffset >= total
                : !isPartial || buffer.byteLength < requestedBytes;
            const reachedViewerCap = nextOffset >= TEXT_MAX_BYTES;
            const hasMore = !reachedFileEnd && !reachedViewerCap;
            const chunk = textDecoder.decode(buffer, { stream: hasMore });

            textContent = reset ? chunk : textContent + chunk;
            textOffset = nextOffset;
            textTotal = total;
            textDone = buffer.byteLength === 0 || !hasMore;
        } catch (error) {
            if (generation === textGeneration) textError = String(error || 'Could not open text file');
        } finally {
            if (generation === textGeneration) textLoading = false;
        }
    }

    async function loadStructuredText(): Promise<void> {
        const url = $fileViewerState.url;
        const mode = currentTextMode();
        if (!$fileViewerState.open || $fileViewerState.kind !== 'text' || !url || mode === 'plain') return;

        const generation = textGeneration;
        textLoading = true;
        textError = '';
        try {
            const response = await fetch(url, {
                headers: { Range: `bytes=0-${TEXT_MAX_BYTES - 1}` },
            });
            if (!isTextResponseOK(response)) {
                throw new Error(`Text request failed (${response.status})`);
            }
            const buffer = await response.arrayBuffer();
            if (generation !== textGeneration) return;

            const isPartial = response.status === 206;
            const total = parseContentRangeTotal(response.headers.get('Content-Range'));
            const nextOffset = buffer.byteLength;
            const reachedFileEnd = total > 0
                ? nextOffset >= total
                : !isPartial || nextOffset < TEXT_MAX_BYTES;
            const reachedViewerCap = nextOffset >= TEXT_MAX_BYTES;
            const source = textDecoder.decode(buffer, { stream: false });
            const { renderStructuredText } = await import('./text-viewer');
            if (generation !== textGeneration) return;

            textContent = source;
            textRenderedHtml = renderStructuredText({
                mode,
                source,
                language: structuredTextLanguageForName($fileViewerState.title) ?? undefined,
            });
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
        activeTextSource = '';
        textDecoder = new TextDecoder('utf-8', { fatal: false });
        textContent = '';
        textOffset = 0;
        textTotal = 0;
        textLoading = false;
        textError = '';
        textDone = false;
        textRenderedHtml = '';
        markdownView = 'rendered';
    }

    function textCompletionLabel(): string {
        return textTotal > 0 && textOffset >= textTotal ? 'End of file' : 'Preview limit';
    }

    function currentTextMode(): TextViewerMode {
        return textViewerModeForName($fileViewerState.title);
    }

    function textProgressLabel(): string {
        const mode = currentTextMode();
        if (mode !== 'plain') return '';
        if (textOffset > 0) return `${textOffset < 1024 ? `${textOffset} B` : `${(textOffset / 1024).toFixed(0)} KB`} loaded`;
        if (!textLoading) return '';
        return 'Opening text…';
    }

    function shouldShowMarkdownToggle(): boolean {
        return currentTextMode() === 'markdown' && (textRenderedHtml.length > 0 || textContent.length > 0);
    }

    function shouldShowTextFooter(): boolean {
        return currentTextMode() === 'plain';
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
        if (nextUrl === activePdfUrl) return;
        activePdfUrl = nextUrl;
        pdfLoading = Boolean(nextUrl);
        pdfError = '';
        pdfPageCount = 0;
    });

    $effect(() => {
        const { open, kind, url, title } = $fileViewerState;
        const nextSource = open && kind === 'text' && url ? `${url}\n${title}` : '';
        if (nextSource === activeTextSource) return;
        resetText();
        activeTextSource = nextSource;
        if (!nextSource) return;
        if (currentTextMode() === 'plain') {
            void loadTextChunk(true);
            return;
        }
        void loadStructuredText();
    });

    $effect(() => {
        if (typeof window === 'undefined') return;

        const handleMessage = (event: MessageEvent): void => {
            if (!pdfFrameEl || event.source !== pdfFrameEl.contentWindow) return;
            if (!isPdfFrameMessage(event.data)) return;

            if (event.data.type === 'loaded') {
                pdfLoading = false;
                pdfError = '';
                pdfPageCount = Math.max(0, Math.floor(event.data.pages));
                return;
            }
            pdfLoading = false;
            pdfPageCount = 0;
            pdfError = event.data.message || 'Could not open PDF';
        };

        window.addEventListener('message', handleMessage);
        return () => window.removeEventListener('message', handleMessage);
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
            {#if shouldShowMarkdownToggle()}
                <div class="text-viewer-toggle" role="tablist" aria-label="Markdown view">
                    <button
                        class:active={markdownView === 'rendered'}
                        type="button"
                        role="tab"
                        aria-selected={markdownView === 'rendered'}
                        onclick={() => { markdownView = 'rendered'; }}
                    >
                        Preview
                    </button>
                    <button
                        class:active={markdownView === 'raw'}
                        type="button"
                        role="tab"
                        aria-selected={markdownView === 'raw'}
                        onclick={() => { markdownView = 'raw'; }}
                    >
                        Raw
                    </button>
                </div>
            {/if}
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
                    onerror={() => {
                        audioWaiting = false;
                        audioError = 'Could not load this audio stream';
                    }}
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
                        <div class="audio-subtitle">{audioError || (audioWaiting ? 'Buffering from Telegram' : audioPaused ? 'Ready to play' : 'Streaming audio')}</div>
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
            <div class="pdf-frame-shell">
                {#if pdfError}
                    <div class="file-viewer-state" role="alert">{pdfError}</div>
                {:else}
                    <iframe
                        bind:this={pdfFrameEl}
                        class="pdf-frame"
                        title={$fileViewerState.title || 'PDF viewer'}
                        src={pdfViewerFrameSrc($fileViewerState.url)}
                    ></iframe>
                    {#if pdfLoading}
                        <div class="pdf-frame-overlay" aria-live="polite">Opening PDF…</div>
                    {/if}
                {/if}
            </div>
        {:else if $fileViewerState.kind === 'text'}
            <div class="text-viewer">
                {#if textError}
                    <div class="file-viewer-state" role="alert">{textError}</div>
                {/if}
                <div class="text-viewer-scroll">
                    {#if currentTextMode() === 'plain'}
                        <pre class="text-viewer-body">{textContent}</pre>
                    {:else if currentTextMode() === 'markdown' && markdownView === 'raw'}
                        <pre class="text-viewer-body">{textContent}</pre>
                    {:else if textRenderedHtml}
                        <div class={`text-viewer-body is-structured is-${currentTextMode()}`}>
                            <!-- eslint-disable-next-line svelte/no-at-html-tags -->
                            {@html textRenderedHtml}
                        </div>
                    {:else}
                        <div class="file-viewer-state">{textLoading ? 'Rendering preview…' : 'No preview available'}</div>
                    {/if}
                </div>
                {#if shouldShowTextFooter()}
                    <div class="text-viewer-footer">
                        <span>{textProgressLabel()}</span>
                        <button class="file-viewer-action" type="button" disabled={textLoading || textDone} onclick={() => void loadTextChunk()}>
                            {textDone ? textCompletionLabel() : textLoading ? 'Loading…' : 'Load more'}
                        </button>
                    </div>
                {/if}
            </div>
        {:else}
            <div class="file-viewer-state">Opening file…</div>
        {/if}
    </div>
</ModalShell>
