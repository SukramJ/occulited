import {expect, test} from '@playwright/test';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {qrY4M} from './qrpng';

// task 154: the live viewfinder, fed by Chromium's fake camera with a sticker's code - it is stored,
// and the viewfinder stays open for the next one (a pile of stickers). 127.0.0.1 is a secure
// context, which the camera needs.
const video = qrY4M('EQ01SG3014F711A0000B1B2C3D4E5FDLK0123456789ABCDEFFEDCBA9876543210', join(tmpdir(), `dk-camera-${process.pid}.y4m`));
test.use({launchOptions: {args: ['--use-fake-ui-for-media-stream', '--use-fake-device-for-media-stream', `--use-file-for-fake-video-capture=${video}`]}, permissions: ['camera']});

test('a sticker held into the camera is stored, and the viewfinder stays for the next', async ({page, baseURL}) => {
    await page.context().addCookies([{name: 'stub-dk', value: `dkc-${Math.random().toString(36).slice(2)}`, url: baseURL!}]);
    await page.goto('/system/keys');
    await page.locator('[data-qr="camera"]').click();
    await expect(page.locator('.qr-view video')).toBeVisible();
    await expect(page.locator('[data-notice="device-key-added"]')).toHaveText('Key stored for Heizung Bad.', {timeout: 10_000});
    await expect(page.locator('[data-count]')).toHaveText('2 of 3 paired HmIP devices have their key here.');
    // the same code again is not reported twice in a row
    await page.waitForTimeout(1500);
    await expect(page.locator('[data-notice="device-key-added"]')).toHaveText('Key stored for Heizung Bad.');
    await expect(page.locator('.qr-view video')).toBeVisible();
    await page.locator('[data-qr="stop"]').click();
    await expect(page.locator('.qr-view')).toHaveCount(0);
});
