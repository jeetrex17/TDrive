// What a tile says and what it shows. The wording is the navigation here --
// a tile a person cannot read is a folder they cannot find -- so the naming,
// the counts and the cover identity are pinned down without a DOM.
import { describe, expect, it } from 'vitest';
import type { MediaFolder } from '../../api/gallery';
import {
    ALBUM_LABEL_HEIGHT, DRIVE_ROOT_LABEL, albumCountLabel, albumGridMetrics, albumTile,
    albumsWorthShowing, buildAlbumTiles,
} from './album-view';

const CHANNEL = 42;

function folder(overrides: Partial<MediaFolder> = {}): MediaFolder {
    return {
        folderId: 'd:camera', name: 'Camera', itemCount: 595, latestUploadTime: 1_700_000_000,
        coverMsgId: 9, coverRevision: 3, coverName: 'IMG_9.jpg',
        ...overrides,
    };
}

describe('album tiles', () => {
    it('names the drive root the way the trash already does', () => {
        const tile = albumTile(folder({ folderId: '', name: '' }), CHANNEL);
        expect(tile.name).toBe(DRIVE_ROOT_LABEL);
        expect(tile.label).toBe(`${DRIVE_ROOT_LABEL}, 595 photos`);
    });

    it('groups thousands and never abbreviates them', () => {
        expect(albumCountLabel(1053)).toBe(`${(1053).toLocaleString()} photos`);
        expect(albumCountLabel(1)).toBe('1 photo');
        expect(albumCountLabel(0)).toBe('0 photos');
        expect(albumCountLabel(-5)).toBe('0 photos');
    });

    it('announces the folder and its size, not just the name', () => {
        expect(albumTile(folder(), CHANNEL).label).toBe('Camera, 595 photos');
    });

    it('addresses the cover through the shared thumbnail identity', () => {
        expect(albumTile(folder(), CHANNEL).cover).toEqual({ channelId: CHANNEL, fileId: 9, revision: 3 });
    });

    it('leaves the cover out when there is nothing renderable to show', () => {
        // No cover at all, a video (no still of its own), a revision the
        // rendition path would refuse, and no drive to address it in.
        expect(albumTile(folder({ coverMsgId: 0, coverName: '' }), CHANNEL).cover).toBeUndefined();
        expect(albumTile(folder({ coverName: 'clip.mp4' }), CHANNEL).cover).toBeUndefined();
        expect(albumTile(folder({ coverRevision: 0 }), CHANNEL).cover).toBeUndefined();
        expect(albumTile(folder(), 0).cover).toBeUndefined();
    });

    it('keeps the backend order rather than re-sorting the same timestamps', () => {
        const tiles = buildAlbumTiles([
            folder({ folderId: 'd:new', name: 'New', latestUploadTime: 9 }),
            folder({ folderId: 'd:old', name: 'Old', latestUploadTime: 1 }),
        ], CHANNEL);
        expect(tiles.map((tile) => tile.folderId)).toEqual(['d:new', 'd:old']);
    });

    it('earns the grid only when there is structure to show', () => {
        expect(albumsWorthShowing([])).toBe(false);
        expect(albumsWorthShowing(buildAlbumTiles([folder()], CHANNEL))).toBe(false);
        expect(albumsWorthShowing(buildAlbumTiles([folder(), folder({ folderId: 'd:shots' })], CHANNEL))).toBe(true);
    });
});

describe('album grid geometry', () => {
    it('fits two columns on a phone and more as the window grows', () => {
        expect(albumGridMetrics(390, 10).columns).toBe(2);
        expect(albumGridMetrics(600, 10).columns).toBe(3);
        expect(albumGridMetrics(1200, 10).columns).toBeGreaterThanOrEqual(6);
        // Never one, however narrow: a single column of squares is a list.
        expect(albumGridMetrics(120, 10).columns).toBe(2);
    });

    it('measures a row as a square cover plus its label strip', () => {
        const metrics = albumGridMetrics(600, 7);
        expect(metrics.rowCount).toBe(3);
        expect(metrics.rowPitch).toBeGreaterThan(metrics.tileWidth + ALBUM_LABEL_HEIGHT);
        expect(albumGridMetrics(600, 0).rowCount).toBe(0);
    });
});
