import { afterEach, describe, expect, it, vi } from 'vitest';
import { render } from 'svelte/server';
import PersonalDriveSetup from './PersonalDriveSetup.svelte';

const noop = () => undefined;

function renderSetup(overrides: Record<string, unknown> = {}) {
    return render(PersonalDriveSetup, {
        props: {
            phase: 'ready',
            candidates: [],
            error: '',
            onSelect: noop,
            onCreate: noop,
            onRetry: noop,
            ...overrides,
        },
    }).body;
}

afterEach(() => vi.restoreAllMocks());

describe('PersonalDriveSetup', () => {
    it('renders an announced loading state', () => {
        const body = renderSetup({ phase: 'loading' });
        expect(body).toContain('role="status"');
        expect(body).toContain('aria-live="polite"');
        expect(body).toContain('Looking for your drives');
    });

    it('renders discovery errors with Retry and no creation escape hatch', () => {
        const body = renderSetup({ phase: 'discovery-error', error: 'Telegram is unavailable.' });
        expect(body).toContain('role="alert"');
        expect(body).toContain('Telegram is unavailable.');
        expect(body).toContain('Retry');
        expect(body).not.toContain('Create New TDrive');
    });

    it('renders an explicit empty state without selecting or creating automatically', () => {
        const body = renderSetup();
        expect(body).toContain('No personal channels found');
        expect(body).toContain('Create New TDrive');
        expect(body).not.toContain('Continue');
    });

    it('shows safe candidate metadata without exposing access hashes', () => {
        const body = renderSetup({
            candidates: [{
                id: '8200',
                title: '<img src=x>',
                created_at: 1_700_000_000,
                has_activity: true,
                recommended: true,
            }],
        });
        expect(body).not.toContain('<img src=x>');
        expect(body).toContain('&lt;img src=x>');
        expect(body).toContain('Channel ID 8200');
        expect(body).toContain('Has activity');
        expect(body).toContain('Recommended');
        expect(body.toLowerCase()).not.toContain('access_hash');
        expect(body.toLowerCase()).not.toContain('access hash');
    });

    it('keeps long titles in a dedicated truncation boundary', () => {
        const body = renderSetup({
            candidates: [{
                id: '8200',
                title: 'TDrive-with-a-very-long-unbroken-channel-name',
                created_at: 0,
                has_activity: false,
                recommended: true,
            }],
        });

        expect(body).toContain('class="drive-title-text ');
        expect(body).toContain('title="TDrive-with-a-very-long-unbroken-channel-name"');
        expect(body).toContain('Recommended');
    });
});
