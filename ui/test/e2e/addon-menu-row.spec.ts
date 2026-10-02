import {type Page} from '@playwright/test';
import {expect, test} from './fixtures';

// openccu-lite task 311 (the maintainer, 2026-10-02, with a screenshot of the Addons dropdown): the
// highlighted name had too much padding, and the actions on the right (↗, pin, ✕, ⚙) were too close
// together, of different heights, the active pin in a box lower than the entry. Now the entry and
// every action share one height, each action is a square of that height - the active pin's box and
// the empty slots that keep the columns too - and the gaps between them, and to the entry, are
// equal; in both themes and on a phone, where the row stays inside the panel.
const menu = (page: Page) => page.locator('.ol-menu').first();
const row = (page: Page, name: string) => menu(page).getByRole('menu').locator('.ol-menurow').filter({has: page.locator('.ol-addonname', {hasText: new RegExp(`^${name}$`)})});

async function menuOpen(page: Page) {
    await menu(page).locator('.ol-menubtn').click();
    await expect(page.locator('nav.ol-addonpanel')).toHaveAttribute('data-phase', 'open');
}

test('an addon row: the entry and its actions one height, the actions square and evenly spaced', async ({page, baseURL}) => {
    await page.context().addCookies([{name: 'stub-prefs', value: `t311-${process.pid}-${Date.now()}`, url: baseURL!}]);
    await page.goto('/');
    // a kept page puts the ✕ column into every row; a pin makes the active box
    await menuOpen(page);
    await row(page, 'RedMatic').getByRole('menuitem').click();
    await menuOpen(page);
    await row(page, 'RedMatic').locator('.ol-pin').click();
    await expect(row(page, 'RedMatic').locator('.ol-pin')).toHaveAttribute('aria-pressed', 'true');
    await expect(row(page, 'RedMatic').getByRole('button', {name: 'Close RedMatic'})).toBeVisible();
    await row(page, 'RedMatic').locator('.ol-menuitem').hover();
    if (process.env.T311_SHOTS) await page.locator('nav.ol-addonpanel').screenshot({path: `${process.env.T311_SHOTS}/${test.info().project.name}.png`});

    const panel = (await page.locator('nav.ol-addonpanel').boundingBox())!;
    for (const r of await menu(page).getByRole('menu').locator('.ol-menurow').all()) {
        const label = (await r.locator('.ol-addonname').textContent()) ?? '?';
        const entry = (await r.locator('.ol-menuitem').boundingBox())!;
        const actions = [];
        for (const a of await r.locator(':scope > .ol-newtab').all()) actions.push((await a.boundingBox())!);
        expect(actions.length, label).toBeGreaterThanOrEqual(4);
        // an inert row has its ? between the name and ↗: the first gap is from there
        const help = r.locator(':scope > .ol-menuhelp');
        const lead = (await help.count()) > 0 ? (await help.boundingBox())! : entry;
        const gaps = [actions[0].x - (lead.x + lead.width)];
        actions.forEach((a, i) => {
            expect(Math.abs(a.height - entry.height), `${label}: action ${i} height ${a.height} vs entry ${entry.height}`).toBeLessThanOrEqual(1);
            expect(Math.abs(a.width - a.height), `${label}: action ${i} ${a.width}x${a.height}`).toBeLessThanOrEqual(1);
            expect(Math.abs(a.y - entry.y), `${label}: action ${i} top`).toBeLessThanOrEqual(1);
            if (i > 0) gaps.push(a.x - (actions[i - 1].x + actions[i - 1].width));
        });
        for (const g of gaps) expect(Math.abs(g - gaps[0]), `${label}: gaps ${gaps.join(', ')}`).toBeLessThanOrEqual(1);
        expect(gaps[0], `${label}: the gap`).toBeGreaterThanOrEqual(2);
        // nothing runs past the panel, not on a phone either
        expect(actions[actions.length - 1].x + actions[actions.length - 1].width, label).toBeLessThanOrEqual(panel.x + panel.width + 0.5);
    }
    // the active pin: its box is the square of the others, filled in the accent
    const pinned = row(page, 'RedMatic').locator('.ol-pin');
    const bg = await pinned.evaluate((el) => getComputedStyle(el).backgroundColor);
    expect(bg).not.toBe('rgba(0, 0, 0, 0)');
});
