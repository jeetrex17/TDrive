/**
 * The photo fixtures and rendition routing the gallery and preview specs share.
 *
 * A rendition is fetched over HTTP from the media server rather than returned
 * through a binding, so a preview test is a route plus a plan -- no Telegram,
 * no real image pipeline. The colours are 2x2 SVGs, which are small enough to
 * inline and distinctive enough to assert on: a test can read the bytes the
 * <img> actually holds and say which photo they belong to.
 */

import { resolves, type MockPlan } from './wails-mock';
import type { Page } from '@playwright/test';

export const RED_BASE64 = 'PHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciIHdpZHRoPSIyIiBoZWlnaHQ9IjIiPjxyZWN0IHdpZHRoPSIyIiBoZWlnaHQ9IjIiIGZpbGw9IiNlMTFkNDgiLz48L3N2Zz4=';
export const BLUE_BASE64 = 'PHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciIHdpZHRoPSIyIiBoZWlnaHQ9IjIiPjxyZWN0IHdpZHRoPSIyIiBoZWlnaHQ9IjIiIGZpbGw9IiMyNTYzZWIiLz48L3N2Zz4=';
export const GOLD_BASE64 = 'PHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciIHdpZHRoPSIyIiBoZWlnaHQ9IjIiPjxyZWN0IHdpZHRoPSIyIiBoZWlnaHQ9IjIiIGZpbGw9IiNmNTllMGIiLz48L3N2Zz4=';

/** Preview bytes are red for the first photo, blue for the second. */
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

/**
 * The timeline the gallery asks for before it asks for anything else: month
 * buckets and their offsets, from which it decides what to render and what to
 * page in. Derived from the photos so a spec states its content once.
 */
export function galleryPlans(photos: Photo[]): Record<string, MockPlan> {
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
    };
}

/**
 * Serves renditions and reports which were asked for -- the returned array is
 * live, so a spec can assert that a thumbnail was reused rather than refetched.
 * `slowFirstPreview` delays the first photo's screen-sized preview, which is how
 * the late-arrival races are made deterministic.
 */
export async function routeRenditions(page: Page, slowFirstPreview = false): Promise<string[]> {
    const requested: string[] = [];
    await page.route('**/mock-renditions/**', async (route) => {
        const url = new URL(route.request().url());
        requested.push(url.pathname);
        if (slowFirstPreview && url.pathname.endsWith('/101/preview')) {
            await new Promise((resolve) => setTimeout(resolve, 1000));
        }
        const color = url.pathname.includes('/102/')
            ? BLUE_BASE64
            : url.pathname.endsWith('/preview') ? RED_BASE64 : GOLD_BASE64;
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
