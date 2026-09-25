import {expect, test, type Locator, type Page} from '@playwright/test';
import {readFileSync} from 'node:fs';

// task 104: on a phone the filters' second row is folded behind a Filters toggle; these tests use its
// controls, so they start with the fold open (the page remembers it per browser)
test.beforeEach(async ({page}) => {
    await page.addInitScript(() => localStorage.setItem('ol.log.filters', 'open'));
});

// task 64: the Log page's download. A button beside Log levels and Journal opens a menu with the two
// formats; each entry is a plain link to /api/system/v1/log/download that carries the page's
// filters, so the file is what the filters select and streams to the disk.

async function openLog(page: Page) {
    await page.goto('/system/log');
    await expect(page.locator('.ol-line').first()).toBeVisible();
}

async function choose(page: Page, label: string, value: string) {
    await page.getByRole('combobox', {name: label}).click();
    await page.getByRole('listbox', {name: label}).getByRole('option', {name: value, exact: true}).click();
}

/** Relative luminance of a computed rgb()/rgba() colour, 0 (black) to 1 (white). */
function luminance(css: string): number {
    const [r = 0, g = 0, b = 0] = (css.match(/[\d.]+/g) ?? []).map(Number);
    return (0.2126 * r + 0.7152 * g + 0.0722 * b) / 255;
}

async function box(l: Locator) {
    return (await l.boundingBox())!;
}

// task 93: the row above the filters holds the boot menu, the download and the settings behind the gear
test('the Download button sits in the head row between the boot menu and the settings, in their style', async ({page}) => {
    await openLog(page);
    const row = page.locator('.lg-head');
    const boot = row.getByRole('button', {name: /^Boot: /});
    const download = row.getByRole('button', {name: 'Download'});
    const gear = row.getByRole('button', {name: 'Log settings'});
    await expect(download).toBeVisible();
    await expect(download.locator('svg.ol-icon')).toHaveCount(1);
    expect(await download.evaluate((e) => e.classList.contains('hmm-button'))).toBe(true);
    const [b, d, g] = [await box(boot), await box(download), await box(gear)];
    // one row, in this order, as tall as its neighbours - on a phone as well
    expect(Math.abs(d.y - b.y)).toBeLessThan(4);
    expect(Math.abs(g.y - d.y)).toBeLessThan(4);
    expect(d.x).toBeGreaterThanOrEqual(b.x + b.width);
    expect(g.x).toBeGreaterThanOrEqual(d.x + d.width);
    expect(Math.abs(d.height - b.height)).toBeLessThan(3);
    expect(Math.abs(g.height - d.height)).toBeLessThan(3);
    expect(g.x + g.width).toBeLessThanOrEqual(page.viewportSize()!.width);
});

test('the menu offers Text and JSON, stays on the screen, and closes on Escape and outside', async ({page}) => {
    page.on('dialog', (dlg) => {
        throw new Error(`native dialog: ${dlg.message()}`);
    });
    await openLog(page);
    const download = page.getByRole('button', {name: 'Download'});
    await expect(download).toHaveAttribute('aria-expanded', 'false');
    await download.click();
    await expect(download).toHaveAttribute('aria-expanded', 'true');
    const menu = page.getByRole('menu', {name: 'Download'});
    const items = menu.getByRole('menuitem');
    await expect(items).toHaveCount(2);
    await expect(items.nth(0)).toContainText('Text');
    await expect(items.nth(0)).toContainText('One line per entry, as journalctl prints it');
    await expect(items.nth(1)).toContainText('JSON');
    await expect(items.nth(1)).toContainText('One JSON object per entry, one per line');
    await expect(menu).toContainText('Everything the filters select, oldest first');
    const m = await box(menu);
    expect(m.x).toBeGreaterThanOrEqual(0);
    expect(m.x + m.width).toBeLessThanOrEqual(page.viewportSize()!.width);
    await page.keyboard.press('Escape');
    await expect(menu).toBeHidden();
    await expect(download).toBeFocused();
    await download.click();
    await expect(menu).toBeVisible();
    await page.locator('h1').click();
    await expect(menu).toBeHidden();
});

test('the links carry the unit, tag, severity, range and text filter, and not the limit', async ({page}) => {
    await openLog(page);
    await choose(page, 'Unit', 'rfd');
    await expect(page).toHaveURL(/\/system\/log\?unit=rfd/);
    await choose(page, 'Tag', 'rfd');
    await page.getByLabel('Severity').selectOption('warning');
    await page.getByRole('button', {name: 'Time range'}).click();
    await page.getByRole('dialog', {name: 'Time range'}).getByRole('button', {name: '1 h', exact: true}).click();
    await page.getByPlaceholder('Text filter').fill('listBidcos');
    await page.getByRole('button', {name: 'Download'}).click();
    for (const format of ['text', 'json']) {
        const link = page.getByRole('menu', {name: 'Download'}).locator(`a[data-format="${format}"]`);
        await expect(link).toHaveAttribute('download', '');
        const url = new URL((await link.getAttribute('href'))!, 'http://box');
        expect(url.pathname).toBe('/api/system/v1/log/download');
        const p = url.searchParams;
        // task 125: the session is no part of the link; the click fetches a ticket for it
        expect(Object.fromEntries(p)).toEqual({unit: 'rfd', tag: 'rfd', severity: 'warning', since: '-1h', q: 'listBidcos', format});
    }
    // a picked range goes as the two epoch instants the page itself sends
    await page.locator('h1').click();
    await page.getByRole('button', {name: 'Time range'}).click();
    const pop = page.getByRole('dialog', {name: 'Time range'});
    await pop.locator('input[type=datetime-local]').nth(0).fill('2026-09-08T10:00');
    await pop.locator('input[type=datetime-local]').nth(1).fill('2026-09-08T12:30');
    await pop.getByRole('button', {name: 'Apply'}).click();
    await page.getByRole('button', {name: 'Download'}).click();
    const href = (await page.locator('a[data-format="text"]').getAttribute('href'))!;
    const p = new URL(href, 'http://box').searchParams;
    expect(Number(p.get('until')!.slice(1)) - Number(p.get('since')!.slice(1))).toBe(150 * 60);
});

test('Text and JSON download files with the expected name and content', async ({page}) => {
    await openLog(page);
    await choose(page, 'Tag', 'rfd');
    // newest first on the page: the file is oldest first all the same
    await page.getByLabel('Order').selectOption('newest');
    const download = page.getByRole('button', {name: 'Download'});
    const menu = page.getByRole('menu', {name: 'Download'});

    await download.click();
    const [text] = await Promise.all([page.waitForEvent('download'), menu.locator('a[data-format="text"]').click()]);
    expect(text.suggestedFilename()).toMatch(/^openccu-log-\d{4}-\d\d-\d\dT\d{4}\.txt$/);
    const lines = readFileSync((await text.path())!, 'utf8').trimEnd().split('\n');
    expect(lines.length).toBe(8);
    for (const l of lines) expect(l).toMatch(/^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\+00:00 openccu rfd\[\d+\]: Sample log line \d+: /);
    const times = lines.map((l) => l.slice(0, 25));
    expect([...times].sort()).toEqual(times);
    await expect(menu).toBeHidden();

    // JSON, with a range: the name carries both ends
    await page.getByRole('button', {name: 'Time range'}).click();
    const pop = page.getByRole('dialog', {name: 'Time range'});
    await pop.locator('input[type=datetime-local]').nth(0).fill('2026-09-08T10:00');
    await pop.locator('input[type=datetime-local]').nth(1).fill('2026-09-08T12:30');
    await pop.getByRole('button', {name: 'Apply'}).click();
    await download.click();
    const [json] = await Promise.all([page.waitForEvent('download'), menu.locator('a[data-format="json"]').click()]);
    expect(json.suggestedFilename()).toMatch(/^openccu-log-\d{4}-\d\d-\d\dT\d{4}-\d{4}-\d\d-\d\dT\d{4}\.jsonl$/);
    const objects = readFileSync((await json.path())!, 'utf8').trimEnd().split('\n').map((l) => JSON.parse(l) as {tag: string; message: string});
    expect(objects.length).toBe(8);
    for (const o of objects) expect(o.tag).toBe('rfd');
});

test('the button and its menu follow the theme', async ({page}, info) => {
    await openLog(page);
    const download = page.getByRole('button', {name: 'Download'});
    // the same look as the settings button beside it
    const look = (l: Locator) =>
        l.evaluate((e) => {
            const s = getComputedStyle(e);
            return [s.backgroundColor, s.color, s.borderTopColor, s.borderTopWidth, s.borderRadius, s.fontSize].join(' | ');
        });
    expect(await look(download)).toBe(await look(page.getByRole('button', {name: 'Log settings'})));
    await download.click();
    const menu = page.getByRole('menu', {name: 'Download'});
    const bg = luminance(await menu.evaluate((e) => getComputedStyle(e).backgroundColor));
    const fg = luminance(await menu.getByRole('menuitem').first().evaluate((e) => getComputedStyle(e).color));
    if (info.project.name.includes('dark')) {
        expect(bg).toBeLessThan(0.4);
        expect(fg).toBeGreaterThan(0.6);
    } else {
        expect(bg).toBeGreaterThan(0.6);
        expect(fg).toBeLessThan(0.4);
    }
});
