/**
 * The photo fixtures and rendition routing the gallery and preview specs share.
 *
 * A rendition is fetched over HTTP from the media server rather than returned
 * through a binding, and an original arrives as a loopback URL the viewer hands
 * straight to the element, so a preview test is a route plus a plan -- no
 * Telegram, no real image pipeline. The colours are 2x2 SVGs, which are small
 * enough to inline and distinctive enough to assert on: a test can read the
 * bytes the <img> actually holds and say which photo they belong to.
 */

import { byFirstArg, resolves, type MockPlan } from './wails-mock';
import type { Page } from '@playwright/test';

export const RED_BASE64 = 'PHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciIHdpZHRoPSIyIiBoZWlnaHQ9IjIiPjxyZWN0IHdpZHRoPSIyIiBoZWlnaHQ9IjIiIGZpbGw9IiNlMTFkNDgiLz48L3N2Zz4=';
export const BLUE_BASE64 = 'PHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciIHdpZHRoPSIyIiBoZWlnaHQ9IjIiPjxyZWN0IHdpZHRoPSIyIiBoZWlnaHQ9IjIiIGZpbGw9IiMyNTYzZWIiLz48L3N2Zz4=';
export const GOLD_BASE64 = 'PHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciIHdpZHRoPSIyIiBoZWlnaHQ9IjIiPjxyZWN0IHdpZHRoPSIyIiBoZWlnaHQ9IjIiIGZpbGw9IiNmNTllMGIiLz48L3N2Zz4=';

/** Original bytes are red for the first photo, blue for the second. */
export const FIRST_PHOTO = {
    name: 'first.jpg',
    size: 120,
    msg_id: 101,
    parent_id: '',
    upload_time: 1_735_689_600,
    uploader_id: 7,
    encrypted: false,
    plaintext_size: 0,
};

export const SECOND_PHOTO = {
    name: 'second.jpg',
    size: 240,
    msg_id: 102,
    parent_id: '',
    upload_time: 1_732_924_800,
    uploader_id: 7,
    encrypted: false,
    plaintext_size: 0,
};

export type Photo = typeof FIRST_PHOTO;

/** The URL an opened original resolves to, which is also the element's src. */
export function originalImageUrl(base64: string): string {
    return `data:image/svg+xml;base64,${base64}`;
}

/**
 * What OpenOriginalImage answers with: a revision-bound capability over the
 * original bytes, opened only because someone explicitly asked to see the
 * photo. Inlining the bytes in the URL keeps the stream out of the rendition
 * path, which is what the gallery specs assert.
 */
export function openedOriginal(file: { name: string; msg_id: number }, base64: string) {
    return {
        token: `original-${file.msg_id}`,
        url: originalImageUrl(base64),
        thumbnail_url: '',
        hls_url: '',
        name: file.name,
        kind: 'image',
        mime_type: 'image/svg+xml',
        supports_range: true,
        info: {
            channel_id: 1,
            file_id: file.msg_id,
            revision: 1,
            name: file.name,
            stored_size: 70,
            plaintext_size: 70,
            encrypted: false,
            multipart: false,
        },
    };
}

/**
 * The timeline the gallery asks for before it asks for anything else: month
 * buckets and their offsets, from which it decides what to render and what to
 * page in. Derived from the photos so a spec states its content once.
 *
 * `slowFirstOriginal` delays the first photo's original, which is how the
 * late-arrival races are made deterministic.
 */
export function galleryPlans(photos: Photo[], slowFirstOriginal = false): Record<string, MockPlan> {
    const buckets: Array<{ key: string; start_index: number; count: number; upload_time: number }> = [];
    photos.forEach((photo, index) => {
        const key = new Date(photo.upload_time * 1000).toISOString().slice(0, 7);
        const previous = buckets[buckets.length - 1];
        if (previous?.key === key) previous.count += 1;
        else buckets.push({ key, start_index: index, count: 1, upload_time: photo.upload_time });
    });
    return {
        GetMediaTimeline: resolves({
            channel_id: 1,
            generation: 'test',
            total_count: photos.length,
            page_size: 128,
            buckets,
            anchors: [{ start_index: 0, cursor: '0' }],
        }),
        ListMediaPage: resolves({
            generation: 'test',
            start_index: 0,
            next_cursor: '',
            items: photos.map((photo) => ({
                ...photo,
                revision: 1,
                content_msg_id: photo.msg_id,
                content_hash: '',
            })),
        }),
        OpenOriginalImage: byFirstArg(Object.fromEntries(photos.map((photo) => [
            String(photo.msg_id),
            resolves(
                openedOriginal(photo, photo.msg_id === SECOND_PHOTO.msg_id ? BLUE_BASE64 : RED_BASE64),
                slowFirstOriginal && photo.msg_id === FIRST_PHOTO.msg_id ? 1000 : 0,
            ),
        ]))),
    };
}

/**
 * The album grid: one named folder and the drive's own root, each holding one
 * of the shared photos. Scoped reads answer with that folder's photo alone, so
 * a spec can tell a scoped grid from the drive-wide one by what is in it.
 */
export function albumPlans(): Record<string, MockPlan> {
    const folderTimeline = (photo: Photo) => resolves({
        channel_id: 1,
        generation: 'test',
        total_count: 1,
        page_size: 128,
        buckets: [{ key: new Date(photo.upload_time * 1000).toISOString().slice(0, 7), start_index: 0, count: 1, upload_time: photo.upload_time }],
        anchors: [],
    });
    const folderPage = (photo: Photo) => resolves({
        generation: 'test',
        start_index: 0,
        next_cursor: '',
        items: [{ ...photo, revision: 1, content_msg_id: photo.msg_id, content_hash: '' }],
    });
    return {
        ListMediaFolders: resolves([
            {
                folder_id: 'd:camera', name: 'Camera', item_count: 1,
                latest_upload_time: FIRST_PHOTO.upload_time,
                cover_msg_id: FIRST_PHOTO.msg_id, cover_revision: 1, cover_name: FIRST_PHOTO.name,
            },
            {
                folder_id: '', name: '', item_count: 1,
                latest_upload_time: SECOND_PHOTO.upload_time,
                cover_msg_id: SECOND_PHOTO.msg_id, cover_revision: 1, cover_name: SECOND_PHOTO.name,
            },
        ]),
        GetMediaFolderTimeline: byFirstArg({
            'd:camera': folderTimeline(FIRST_PHOTO),
            '': folderTimeline(SECOND_PHOTO),
        }),
        ListMediaFolderPage: byFirstArg({
            'd:camera': folderPage(FIRST_PHOTO),
            '': folderPage(SECOND_PHOTO),
        }),
    };
}

/**
 * Serves renditions and reports which were asked for -- the returned array is
 * live, so a spec can assert that a thumbnail was reused rather than refetched,
 * and that opening a photo never reached for a rendition of the original.
 */
export async function routeRenditions(page: Page): Promise<string[]> {
    const requested: string[] = [];
    await page.route('**/mock-renditions/**', async (route) => {
        const url = new URL(route.request().url());
        requested.push(url.pathname);
        const color = url.pathname.includes('/102/') ? BLUE_BASE64 : GOLD_BASE64;
        await route.fulfill({
            body: Buffer.from(color, 'base64'),
            contentType: 'image/svg+xml',
            headers: { 'X-Rendition-Width': '2', 'X-Rendition-Height': '2' },
        }).catch(() => {});
    });
    return requested;
}

/** The bytes the preview element is actually holding, not the URL it was given. */
export function previewImageContents(page: Page): Promise<string> {
    return page.locator('#preview-image').evaluate(
        (image) => fetch((image as HTMLImageElement).src).then((response) => response.text()),
    );
}
