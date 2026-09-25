import {expect, test} from '@playwright/test';

// The maintainer, 2026-09-20: the radio firmware moved to the Updates page, each module with its
// own upload; the Interfaces page keeps the running version per module and a pill that leads there
// while a newer file waits. The Updates page carries three panels.

test('the module cards name the running version, with a pill to the Updates page', async ({page}) => {
    await page.goto('/system/interfaces');
    const card = page.locator('#ol-module-BidCos-RF');
    await expect(card).toContainText('4.4.18');
    const pill = card.locator('[data-fw-update="BidCos-RF"]');
    await expect(pill).toHaveText('update available');
    await pill.click();
    await expect(page).toHaveURL(/\/system\/updates#radio-firmware$/);
    await expect(page.getByRole('heading', {name: 'Radio firmware'})).toBeVisible();
});

test('the Updates page has the system update, the radio firmware and the device firmware, each in a panel', async ({page}) => {
    await page.goto('/system/updates');
    for (const panel of ['system-update', 'radio-firmware', 'device-firmware']) {
        await expect(page.locator(`[data-panel="${panel}"]`)).toBeVisible();
    }
    // in that order down the page
    const tops = await page.locator('[data-panel]').evaluateAll((els) => els.map((e) => [e.getAttribute('data-panel'), Math.round(e.getBoundingClientRect().top)] as const));
    expect(tops.map((x) => x[0])).toEqual(['system-update', 'radio-firmware', 'device-firmware']);
    // the module's own upload sits in its panel, and there is no module select any more
    await expect(page.locator('.fw-module .fw-upload')).toHaveCount(1);
    await expect(page.locator('.fw-upload select')).toHaveCount(0);
});

test('the Interfaces page has no firmware section any more', async ({page}) => {
    await page.goto('/system/interfaces');
    await expect(page.getByRole('heading', {name: 'Radio firmware'})).toHaveCount(0);
    await expect(page.locator('.fw-module')).toHaveCount(0);
});
