// Pure view logic for the album grid: what a tile says, which thumbnail it
// shows, and how wide the grid is. Everything here is a function of the folder
// rows plus the viewport width, so the components stay renderers and the
// wording, the counts and the column arithmetic are testable without a DOM.
import type { MediaFolder } from '../../api/gallery';
import { fileThumbnailIdentity } from '../../modules/file-list';
import type { FileThumbnailIdentity } from '../file-list/types';
import type { RowMetrics } from '../file-list/row-window';

/**
 * The drive's own root has no name of its own. The trash already calls this
 * place "Drive root"; a second wording for it would read as a second place.
 */
export const DRIVE_ROOT_LABEL = 'Drive root';

/**
 * Tile geometry. A cover is square because a 4:3 crop of a portrait photo
 * loses the subject, and the label sits under it on a solid strip -- a cover
 * is arbitrary user content and can be any brightness, so text over it would
 * need a scrim idiom this flat UI does not have.
 *
 * The label height is fixed rather than measured: the window below has to know
 * a row's height before the row exists, and two lines of set sizes is a height
 * we choose, not one we discover. The stylesheet reads it back from
 * --album-label-height, so there is one number, not two that can drift.
 */
export const ALBUM_LABEL_HEIGHT = 58;
export const ALBUM_TILE_GAP = 12;
/** Yields 2 columns at 390px and 3 at 600px, then one more every ~184px. */
export const ALBUM_TILE_MIN_WIDTH = 172;

export interface AlbumTile {
    /** '' for the drive root; the scope key for the folder's own timeline. */
    folderId: string;
    /** What the tile prints: the folder name, or the root's label. */
    name: string;
    count: number;
    /** "1,053 photos" -- grouped, never abbreviated: the exact figure is the point. */
    countLabel: string;
    /** What the tile announces: "Camera, 595 photos". */
    label: string;
    /**
     * The cover's thumbnail identity, or undefined when there is nothing to
     * render -- no cover, or a stale revision. A video has one too: the frame
     * drawn for it at upload. The tile draws a folder glyph when there is
     * nothing to address, never a broken image.
     */
    cover?: FileThumbnailIdentity;
}

/** Grouped, and singular when there is one. Never "1.1k": people count photos. */
export function albumCountLabel(count: number): string {
    return count === 1 ? '1 photo' : `${Math.max(0, count).toLocaleString()} photos`;
}

export function albumTileName(folder: Pick<MediaFolder, 'folderId' | 'name'>): string {
    return folder.name || (folder.folderId ? 'Untitled folder' : DRIVE_ROOT_LABEL);
}

/**
 * One tile. The cover goes through the same identity the file list builds, so
 * covers share the rendition broker's leases, byte caps and cancellation with
 * every other thumbnail on screen instead of decoding a second way.
 */
export function albumTile(folder: MediaFolder, channelId: number): AlbumTile {
    const name = albumTileName(folder);
    const countLabel = albumCountLabel(folder.itemCount);
    return {
        folderId: folder.folderId,
        name,
        count: folder.itemCount,
        countLabel,
        label: `${name}, ${countLabel}`,
        cover: fileThumbnailIdentity(
            { msgId: folder.coverMsgId, name: folder.coverName, revision: folder.coverRevision },
            channelId,
        ),
    };
}

/**
 * The grid, in the order the backend gave it (newest folder first). One pass,
 * no sort: the rank is the backend's, and recomputing it here would only be a
 * second opinion about the same timestamps.
 */
export function buildAlbumTiles(folders: readonly MediaFolder[], channelId: number): AlbumTile[] {
    return folders.map((folder) => albumTile(folder, channelId));
}

/**
 * Albums earn the grid only when there is structure to show. One folder is the
 * drive itself, and a grid of one tile is a worse timeline than the timeline.
 */
export function albumsWorthShowing(tiles: readonly AlbumTile[]): boolean {
    return tiles.length > 1;
}

export interface AlbumGridMetrics {
    columns: number;
    /** Cover edge, in CSS pixels; the tile is this wide and label-height taller. */
    tileWidth: number;
    rowCount: number;
    /** Row height including the gap below it, which is what the window measures. */
    rowPitch: number;
}

/** Column count and row geometry for a container width. */
export function albumGridMetrics(width: number, count: number): AlbumGridMetrics {
    const usable = Math.max(1, width);
    const columns = Math.max(2, Math.floor((usable + ALBUM_TILE_GAP) / (ALBUM_TILE_MIN_WIDTH + ALBUM_TILE_GAP)));
    const tileWidth = Math.max(1, (usable - ALBUM_TILE_GAP * (columns - 1)) / columns);
    return {
        columns,
        tileWidth,
        rowCount: Math.ceil(Math.max(0, count) / columns),
        rowPitch: tileWidth + ALBUM_LABEL_HEIGHT + ALBUM_TILE_GAP,
    };
}

/** The uniform metrics the shared row window measures this grid with. */
export function albumRowMetrics(metrics: AlbumGridMetrics): RowMetrics {
    return { rowHeight: metrics.rowPitch, tallRowHeight: metrics.rowPitch, tallIndices: [] };
}
