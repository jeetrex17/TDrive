<script lang="ts">
    import ArrowRightIcon from '@lucide/svelte/icons/arrow-right';
    import ChevronLeftIcon from '@lucide/svelte/icons/chevron-left';
    import ChevronRightIcon from '@lucide/svelte/icons/chevron-right';
    import DownloadIcon from '@lucide/svelte/icons/download';
    import EyeIcon from '@lucide/svelte/icons/eye';
    import EyeOffIcon from '@lucide/svelte/icons/eye-off';
    import InfoIcon from '@lucide/svelte/icons/info';
    import LockIcon from '@lucide/svelte/icons/lock';
    import XIcon from '@lucide/svelte/icons/x';
</script>

<div id="preview-shell" class="preview-shell" role="dialog" aria-modal="true" aria-labelledby="preview-filename">
    <div class="preview-chrome">
        <div id="preview-filename" class="preview-filename" aria-live="polite"></div>
        <div class="preview-chrome-actions">
            <button id="preview-info-btn" class="preview-icon-btn" type="button" aria-label="Photo info" aria-pressed="false" title="Info (i)">
                <InfoIcon aria-hidden="true" />
            </button>
            <button id="preview-download" class="preview-icon-btn" type="button" aria-label="Download" title="Download">
                <DownloadIcon aria-hidden="true" />
            </button>
            <button id="preview-close" class="preview-close-btn" type="button" aria-label="Close preview" title="Close preview">
                <XIcon aria-hidden="true" />
            </button>
        </div>
    </div>

    <button id="preview-prev" class="preview-nav preview-nav-prev" type="button" aria-label="Previous image" hidden>
        <ChevronLeftIcon aria-hidden="true" />
    </button>
    <button id="preview-next" class="preview-nav preview-nav-next" type="button" aria-label="Next image" hidden>
        <ChevronRightIcon aria-hidden="true" />
    </button>

    <div id="preview-counter" class="preview-counter" hidden></div>

    <div id="preview-stage" class="preview-content">
        <div id="preview-loading" class="preview-loading" aria-hidden="true" style="display: none;">
            <div class="preview-loading-track" aria-hidden="true">
                <div id="preview-loading-fill" class="preview-loading-fill"></div>
            </div>
        </div>
        <div id="preview-error" class="preview-error" style="display: none;" role="alert"></div>
        <img id="preview-image" src="" alt="Preview" hidden>
    </div>

    <aside id="preview-info" class="preview-info" aria-label="Photo info">
        <button id="preview-info-close" class="preview-info-close" type="button" aria-label="Close info" title="Close info">
            <XIcon size={14} aria-hidden="true" />
        </button>
        <div id="preview-info-body"></div>
    </aside>

    <div id="preview-locked" class="preview-locked" style="display: none;" role="group" aria-label="Locked photo">
        <div class="preview-unlock">
            <div class="preview-unlock-bar">
                <LockIcon class="preview-unlock-lock" strokeWidth={1.7} aria-hidden="true" />
                <input id="preview-locked-input" type="password" placeholder="Password" autocomplete="current-password" spellcheck="false" aria-label="Encryption password">
                <button id="preview-locked-eye" class="preview-unlock-eye" type="button" aria-label="Show password" aria-pressed="false">
                    <EyeIcon class="preview-eye-show" strokeWidth={1.8} aria-hidden="true" />
                    <EyeOffIcon class="preview-eye-hide" strokeWidth={1.8} aria-hidden="true" />
                </button>
                <button id="preview-locked-unlock" class="preview-unlock-go" type="button" aria-label="Unlock">
                    <ArrowRightIcon strokeWidth={2.2} aria-hidden="true" />
                </button>
            </div>
            <div id="preview-locked-error" class="preview-unlock-error" style="display: none;" role="alert"></div>
            <div id="preview-locked-hint" class="preview-unlock-hint" style="display: none;">Hint: <span id="preview-locked-hint-text"></span></div>
        </div>
    </div>
</div>

<style>
    :global(#preview-locked-eye .preview-eye-hide) {
        display: none;
    }

    :global(#preview-locked-eye[aria-pressed="true"] .preview-eye-show) {
        display: none;
    }

    :global(#preview-locked-eye[aria-pressed="true"] .preview-eye-hide) {
        display: block;
    }
</style>
