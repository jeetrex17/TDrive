import type { Locator, Page } from '@playwright/test';
import { bootTDrive, expect, test } from './wails-mock';

async function expectHiddenScrollbars(page: Page, region: Locator, horizontal: boolean): Promise<void> {
    await expect(region).toBeVisible();
    // Force overflow predictably while keeping the region's own scroll styles.
    const overflow = await region.evaluate((element, horizontal) => {
        const contents = document.createElement('div');
        contents.style.flex = 'none';
        contents.style.width = horizontal ? `${element.clientWidth + 300}px` : '1px';
        contents.style.height = `${element.clientHeight + 300}px`;
        element.append(contents);
        return {
            horizontal: element.scrollWidth > element.clientWidth,
            vertical: element.scrollHeight > element.clientHeight,
        };
    }, horizontal);
    expect(overflow).toEqual({ horizontal, vertical: true });

    await region.hover();
    await page.mouse.wheel(horizontal ? 120 : 0, 120);
    await expect.poll(() => region.evaluate(element => ({
        horizontal: element.scrollLeft > 0,
        vertical: element.scrollTop > 0,
    }))).toEqual({ horizontal, vertical: true });

    const hidden = await region.evaluate(element => {
        if (CSS.supports('scrollbar-width', 'none')) {
            return getComputedStyle(element).getPropertyValue('scrollbar-width') === 'none';
        }
        const scrollbar = getComputedStyle(element, '::-webkit-scrollbar');
        return scrollbar.display === 'none' || (scrollbar.width === '0px' && scrollbar.height === '0px');
    });
    expect(hidden, 'both scrollbar tracks are hidden even while scrolling').toBe(true);
}

test('drive navigation scrolls in both directions with hidden scrollbars', async ({ page }) => {
    await bootTDrive(page);
    await expectHiddenScrollbars(page, page.getByRole('navigation', { name: 'Drives' }).locator('.drives-scroll'), true);
});

test('the standalone PDF viewer scrolls in both directions with hidden scrollbars', async ({ page }) => {
    await page.goto('/pdf-viewer.html');
    await expectHiddenScrollbars(page, page.locator('#viewerContainer'), true);
});

for (const { name, className, horizontal } of [
    { name: 'video track list', className: 'video-track-list', horizontal: true },
    { name: 'video popover', className: 'video-popover-body', horizontal: false },
]) {
    test(`the ${name} scrolls with hidden scrollbars`, async ({ page }) => {
        await bootTDrive(page);
        // Isolate the player styles without depending on video decoding or track availability.
        await page.evaluate(className => {
            const region = document.createElement('div');
            region.className = className;
            region.style.position = 'fixed';
            region.style.top = '100px';
            region.style.left = '400px';
            region.style.width = '160px';
            region.style.height = '100px';
            region.style.zIndex = '10000';
            document.body.append(region);
        }, className);
        await expectHiddenScrollbars(page, page.locator(`body > .${className}`), horizontal);
    });
}
