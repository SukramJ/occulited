import {type Page} from '@playwright/test';
import {expect, test} from './fixtures';

// openccu-lite task 240 (the maintainer): "i would like to have 2 lines here 'Gerät HM-MOD-RPI-PCB' and
// next line 'Via HB-RF-ETH@192.0.2.209' similar for hb-rf-usb* attached devices". The carrier comes
// from the radio detection's device type (lib/radiocarrier.ts); a USB stick that is the radio itself has
// no Via line.

async function modules(page: Page, types: Record<string, [string, string]>) {
    await page.route('**/api/system/v1/radio', async (route) => {
        const res = await route.fetch();
        const r = await res.json();
        for (const m of r.modules ?? []) {
            const t = types[m.protocol];
            if (t) [m.device, m.device_type] = t;
        }
        await route.fulfill({response: res, json: r});
    });
}

const row = (page: Page, protocol: string, label: string) => page.locator(`#ol-module-${protocol} dt`, {hasText: new RegExp(`^${label}$`)});

test('a module on the HB-RF-ETH and one on an HB-RF-USB: the device, then Via on its own line', async ({page}) => {
    await modules(page, {'HmIP-RF': ['HM-MOD-RPI-PCB', 'HB-RF-ETH@192.0.2.209'], 'BidCos-RF': ['HM-MOD-RPI-PCB', 'HB-RF-USB-TK@usb-0000:01:00.0-1.3']});
    await page.goto('/system/interfaces');
    const hmip = page.locator('#ol-module-HmIP-RF');
    await expect(hmip.locator('[data-module-device]')).toHaveText('HM-MOD-RPI-PCB');
    await expect(hmip.locator('[data-module-via]')).toHaveText('HB-RF-ETH@192.0.2.209');
    await expect(row(page, 'HmIP-RF', 'Via')).toHaveCount(1);
    await expect(page.locator('#ol-module-BidCos-RF [data-module-via]')).toHaveText('HB-RF-USB-TK@usb-0000:01:00.0-1.3');
    // two lines: Via below the device
    const dev = (await hmip.locator('[data-module-device]').boundingBox())!;
    const via = (await hmip.locator('[data-module-via]').boundingBox())!;
    expect(via.y).toBeGreaterThanOrEqual(dev.y + dev.height - 1);
    const overflow = await hmip.evaluate((el) => el.scrollWidth > el.clientWidth + 1);
    expect(overflow).toBe(false);
});

test('a module on the GPIO header says GPIO; a USB stick that is the radio has no Via line', async ({page}) => {
    await modules(page, {'HmIP-RF': ['RPI-RF-MOD', 'GPIO@3f201000.serial'], 'BidCos-RF': ['HMIP-RFUSB', 'eQ-3 HmIP-RFUSB@usb-0000:02:1b.0-1']});
    await page.goto('/system/interfaces');
    await expect(page.locator('#ol-module-HmIP-RF [data-module-via]')).toHaveText('GPIO');
    await expect(page.locator('#ol-module-HmIP-RF [data-module-via]')).toHaveAttribute('title', 'GPIO@3f201000.serial');
    await expect(page.locator('#ol-module-BidCos-RF [data-module-device]')).toHaveText('HMIP-RFUSB');
    await expect(page.locator('#ol-module-BidCos-RF [data-module-via]')).toHaveCount(0);
});

test('German: Gerät and Via', async ({page}) => {
    await page.addInitScript(() => localStorage.setItem('ol.language', 'de'));
    await modules(page, {'HmIP-RF': ['HM-MOD-RPI-PCB', 'HB-RF-ETH@192.0.2.209']});
    await page.goto('/system/interfaces');
    await expect(row(page, 'HmIP-RF', 'Gerät')).toHaveCount(1);
    await expect(row(page, 'HmIP-RF', 'Via')).toHaveCount(1);
});
