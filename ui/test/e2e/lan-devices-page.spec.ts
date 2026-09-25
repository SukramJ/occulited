import {expect, test, type Page} from '@playwright/test';

// openccu-lite task 222 (the maintainer, 2026-09-24): "move lan-devices to their own page, including
// bidcos-gateways and hmip-access-points", and "move the usb-devices, add gateway and search again
// buttons below the area headings". System -> LAN devices, after Interfaces in the menu; the
// Interfaces page keeps the radio and links there; the old anchors lead to the new page.

/** each section button: directly below its heading, left-aligned with it, above the section's first card */
async function underHeading(page: Page, heading: string, button: string, firstCard: string) {
    const h = (await page.locator(heading).boundingBox())!;
    const b = (await page.locator('.ol-headrow', {has: page.locator(heading)}).getByRole('button', {name: button}).boundingBox())!;
    const c = (await page.locator(firstCard).first().boundingBox())!;
    expect(b.y, `${button} below ${heading}`).toBeGreaterThanOrEqual(h.y + h.height);
    expect(b.y - (h.y + h.height), `${button} directly below`).toBeLessThan(16);
    expect(Math.abs(b.x - h.x), `${button} left-aligned`).toBeLessThan(1);
    expect(b.y + b.height, `${button} above the first card`).toBeLessThan(c.y);
}

test('the page: four sections, each button under its heading', async ({page}) => {
    await page.goto('/system/lan-devices');
    await expect(page.locator('h1')).toContainText('LAN devices');
    await expect(page.locator('main h2')).toHaveText(['BidCoS Gateways', 'Network radio board (HB-RF-ETH)', 'HmIP Access Points', 'LAN devices']);
    await expect(page.locator('[data-lan-device]').first()).toBeVisible();
    await underHeading(page, 'h2#gateways', 'Add gateway', '.ol-card[data-gateway]');
    await underHeading(page, 'h2#lan-devices', 'Search', '[data-lan-device]');
    // the same spacing under both headings
    const gap = async (id: string) => {
        const h = (await page.locator(`h2#${id}`).boundingBox())!;
        const b = (await page.locator('.ol-headrow', {has: page.locator(`h2#${id}`)}).locator('.ol-headrow-actions').boundingBox())!;
        return Math.round(b.y - (h.y + h.height));
    };
    expect(await gap('gateways')).toBe(await gap('lan-devices'));
});

test('the Interfaces page keeps the radio, without the three sections, and links to the new page', async ({page}) => {
    await page.goto('/system/interfaces');
    await expect(page.locator('h2#modules')).toBeVisible();
    await expect(page.locator('h2#connections')).toBeVisible();
    for (const id of ['gateways', 'access-points', 'lan-devices']) await expect(page.locator(`h2#${id}`)).toHaveCount(0);
    await underHeading(page, 'h2#modules', 'USB devices', '[id^="ol-module-"]');
    const way = page.locator('[data-lan-link] a');
    await expect(way).toHaveAttribute('href', '/system/lan-devices');
    await way.click();
    await expect(page).toHaveURL(/\/system\/lan-devices$/);
    await expect(page.locator('h2#gateways')).toBeVisible();
});

for (const [from, to] of [
    ['/system/interfaces#gateways', '/system/lan-devices#gateways'],
    ['/radio#access-points', '/system/lan-devices#access-points'],
    ['/system/interfaces#lan-devices', '/system/lan-devices#lan-devices'],
] as const) {
    test(`the old ${from} leads to ${to}`, async ({page, baseURL}) => {
        await page.goto(from);
        await expect(page).toHaveURL(`${baseURL}${to}`);
        await expect(page.locator(`h2${to.slice(to.indexOf('#'))}`)).toBeVisible();
    });
}

test('the System menu lists it after Interfaces', async ({page}) => {
    await page.goto('/system/interfaces');
    await page.getByRole('button', {name: /^Interfaces/}).first().click();
    const rows = page.locator('[role="menuitem"]');
    await expect(rows.nth(0)).toContainText('Interfaces');
    await expect(rows.nth(1)).toContainText('LAN devices');
    await rows.nth(1).click();
    await expect(page).toHaveURL(/\/system\/lan-devices$/);
});

test('a user sees the lists without the buttons that change something', async ({page}) => {
    await page.route('**/api/auth/v1/state', (r) => r.fulfill({json: {setup_required: false, authenticated: true, user: 'monitor', role: 'user', must_change_password: false, sid: 'U1'}}));
    await page.goto('/system/lan-devices');
    await expect(page.locator('.ol-card[data-gateway]').first()).toBeVisible();
    await expect(page.getByRole('button', {name: 'Add gateway'})).toHaveCount(0);
    await expect(page.locator('[data-gateway] button')).toHaveCount(0);
});

test('in German', async ({page}) => {
    await page.addInitScript(() => localStorage.setItem('ol.language', 'de'));
    await page.goto('/system/lan-devices');
    await expect(page.locator('main h2')).toHaveText(['BidCoS-Gateways', 'Netzwerk-Funkplatine (HB-RF-ETH)', 'HmIP-Access-Points', 'LAN-Geräte']);
    await expect(page.getByRole('button', {name: 'Gateway hinzufügen'})).toBeVisible();
    await expect(page.getByRole('button', {name: 'Suchen', exact: true})).toBeVisible();
});
