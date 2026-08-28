import { describe, expect, it } from 'vitest';
import { get } from 'svelte/store';
import { parseDriveScanProgress, personalDriveSetup } from './personal-drive-store';

const applying = {
    phase: 'applying' as const,
    pages_done: 3,
    pages_total: 12,
    messages_done: 300,
    messages_total: 1170,
    wait_seconds: 0,
};

describe('parseDriveScanProgress', () => {
    it('keeps recognised phases and coerces counters', () => {
        expect(parseDriveScanProgress({ ...applying, messages_done: 12.9, pages_done: -4 })).toEqual({
            ...applying,
            messages_done: 12,
            pages_done: 0,
        });
    });

    it('rejects payloads the picker cannot render', () => {
        for (const payload of [null, undefined, 'applying', 42, {}, { phase: 'finished' }]) {
            expect(parseDriveScanProgress(payload)).toBeNull();
        }
    });
});

describe('personalDriveSetup scan progress', () => {
    it('ignores scans while no recovery is on screen', () => {
        personalDriveSetup.showCandidates([]);
        personalDriveSetup.scanProgress(applying);
        expect(get(personalDriveSetup).scan).toBeNull();
    });

    it('tracks the latest counters once recovery starts', () => {
        personalDriveSetup.showCandidates([]);
        personalDriveSetup.recovering();
        personalDriveSetup.scanProgress(applying);
        expect(get(personalDriveSetup).scan).toEqual(applying);
    });

    it('keeps counters through a rate-limit pause and clears the wait after it', () => {
        personalDriveSetup.showCandidates([]);
        personalDriveSetup.recovering();
        personalDriveSetup.scanProgress(applying);

        personalDriveSetup.scanProgress({ ...applying, phase: 'waiting', wait_seconds: 30 });
        expect(get(personalDriveSetup)).toMatchObject({ scan: applying, waitSeconds: 30 });

        personalDriveSetup.scanProgress({ ...applying, messages_done: 400 });
        expect(get(personalDriveSetup)).toMatchObject({
            scan: { messages_done: 400 },
            waitSeconds: 0,
        });
    });

    it('drops stale counters when a new recovery starts', () => {
        personalDriveSetup.showCandidates([]);
        personalDriveSetup.recovering();
        personalDriveSetup.scanProgress(applying);
        personalDriveSetup.recovering();
        expect(get(personalDriveSetup)).toMatchObject({ scan: null, waitSeconds: 0 });
    });
});
