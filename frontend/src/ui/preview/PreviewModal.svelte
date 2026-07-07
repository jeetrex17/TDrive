<div id="preview-shell" class="preview-shell" role="dialog" aria-modal="true" aria-labelledby="preview-filename">
    <div class="preview-chrome">
        <div id="preview-filename" class="preview-filename" aria-live="polite"></div>
        <div class="preview-chrome-actions">
            <button id="preview-info-btn" class="preview-icon-btn" type="button" aria-label="Photo info" aria-pressed="false" title="Info (i)">
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="9"/><path stroke-linecap="round" stroke-linejoin="round" d="M12 11.5v4.5"/><circle cx="12" cy="7.8" r="0.6" fill="currentColor" stroke="none"/></svg>
            </button>
            <button id="preview-download" class="preview-icon-btn" type="button" aria-label="Download" title="Download">
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path stroke-linecap="round" stroke-linejoin="round" d="M12 3v12m0 0l-4-4m4 4l4-4M5 21h14"/></svg>
            </button>
            <button id="preview-close" class="preview-close-btn" type="button" aria-label="Close preview" title="Close preview">×</button>
        </div>
    </div>

    <button id="preview-prev" class="preview-nav preview-nav-prev" type="button" aria-label="Previous image" hidden>
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path stroke-linecap="round" stroke-linejoin="round" d="M15 19l-7-7 7-7"/></svg>
    </button>
    <button id="preview-next" class="preview-nav preview-nav-next" type="button" aria-label="Next image" hidden>
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path stroke-linecap="round" stroke-linejoin="round" d="M9 5l7 7-7 7"/></svg>
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
        <button id="preview-info-close" class="preview-info-close" type="button" aria-label="Close info" title="Close info">×</button>
        <div id="preview-info-body"></div>
    </aside>

    <div id="preview-locked" class="preview-locked" style="display: none;" role="group" aria-label="Locked photo">
        <div class="preview-unlock">
            <div class="preview-unlock-bar">
                <svg class="preview-unlock-lock" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" aria-hidden="true">
                    <rect x="5" y="11" width="14" height="9" rx="2.5"/>
                    <path stroke-linecap="round" d="M8 11V8a4 4 0 018 0v3"/>
                </svg>
                <input id="preview-locked-input" type="password" placeholder="Password" autocomplete="current-password" spellcheck="false" aria-label="Encryption password">
                <button id="preview-locked-eye" class="preview-unlock-eye" type="button" aria-label="Show password" aria-pressed="false">
                    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true">
                        <path stroke-linecap="round" stroke-linejoin="round" d="M2.5 12S6 5.5 12 5.5s9.5 6.5 9.5 6.5-3.5 6.5-9.5 6.5S2.5 12 2.5 12Z"/>
                        <circle cx="12" cy="12" r="3.2"/>
                    </svg>
                </button>
                <button id="preview-locked-unlock" class="preview-unlock-go" type="button" aria-label="Unlock">
                    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" aria-hidden="true"><path stroke-linecap="round" stroke-linejoin="round" d="M4 12h15m-6-6 6 6-6 6"/></svg>
                </button>
            </div>
            <div id="preview-locked-error" class="preview-unlock-error" style="display: none;" role="alert"></div>
            <div id="preview-locked-hint" class="preview-unlock-hint" style="display: none;">Hint: <span id="preview-locked-hint-text"></span></div>
        </div>
    </div>
</div>
