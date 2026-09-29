import {test, expect, type Page} from '@playwright/test';

// task 104: on a phone the filters' second row is folded behind a Filters toggle; these tests use its
// controls, so they start with the fold open (the page remembers it per browser)
test.beforeEach(async ({page}) => {
    await page.addInitScript(() => localStorage.setItem('ol.log.filters', 'open'));
});

// task 40: the Unit and Tag dropdowns are the shell's own filterable select (lib/Select.svelte):
// a combobox trigger that names the choice, a popover with a filter input and a listbox
async function choose(page: Page, label: string, value: string) {
    await page.getByRole('combobox', {name: label}).click();
    await page.getByRole('listbox', {name: label}).getByRole('option', {name: value, exact: true}).click();
}
async function optionCount(page: Page, label: string) {
    await page.getByRole('combobox', {name: label}).click();
    const n = await page.getByRole('listbox', {name: label}).getByRole('option').count();
    await page.keyboard.press('Escape');
    return n;
}

// task 31: the Log page's filters as requests, the time range, the order
test('the filters reach the request, and a chosen tag does not shrink the tag list', async ({page}) => {
    const reqs: string[] = [];
    page.on('request', (r) => {
        if (r.url().includes('/api/system/v1/log?')) reqs.push(new URL(r.url()).search);
    });
    await page.goto('/system/log');
    await expect(page.locator('.ol-line').first()).toBeVisible();
    const before = await optionCount(page, 'Tag');
    expect(before).toBeGreaterThan(2);
    await choose(page, 'Tag', 'rfd');
    await expect.poll(() => reqs.at(-1)).toContain('tag=rfd');
    await expect(page.getByRole('combobox', {name: 'Tag'})).toContainText('Tag: rfd');
    // the stub answers the same lines whatever the filter; the list must still hold every tag seen
    await expect.poll(() => optionCount(page, 'Tag')).toBe(before);
    await choose(page, 'Unit', 'rfd');
    await expect.poll(() => reqs.at(-1)).toMatch(/tag=.*unit=|unit=.*tag=/);
    await expect(page).toHaveURL(/\/system\/log\?unit=rfd/);
    await page.getByLabel('Severity').selectOption('warning');
    await expect.poll(() => reqs.at(-1)).toContain('severity=warning');
});

test('the Unit dropdown: a focused filter, arrow keys, Enter, the route and the request', async ({page}) => {
    const reqs: string[] = [];
    page.on('request', (r) => {
        if (r.url().includes('/api/system/v1/log?')) reqs.push(new URL(r.url()).search);
    });
    await page.goto('/system/log');
    await expect(page.locator('.ol-line').first()).toBeVisible();
    const btn = page.getByRole('combobox', {name: 'Unit'});
    await expect(btn).toContainText('Unit: all');
    await btn.click();
    await expect(btn).toHaveAttribute('aria-expanded', 'true');
    const filter = page.getByRole('searchbox', {name: 'Filter'});
    await expect(filter).toBeFocused();
    const list = page.getByRole('listbox', {name: 'Unit'});
    // the stub's services: occulited, rfd, hmipserver, addon-mosquitto (and the lines' units)
    await filter.fill('rf');
    const names = await list.getByRole('option').allTextContents();
    expect(names.length).toBeGreaterThan(0);
    for (const n of names) expect(n.toLowerCase()).toContain('rf');
    // a "no match" line rather than an empty box
    await filter.fill('zzz-nothing');
    await expect(list.getByRole('option')).toHaveCount(0);
    await expect(list).toContainText('no match');
    await filter.fill('');
    // ↓↓ Enter chooses the second entry after "all"
    const all = await list.getByRole('option').allTextContents();
    await filter.press('ArrowDown');
    await filter.press('ArrowDown');
    await expect(list.getByRole('option').nth(2)).toHaveClass(/hl/);
    await expect(btn).toHaveAttribute('aria-activedescendant', /-2$/);
    await filter.press('Enter');
    const chosen = all[2]!.trim();
    await expect(btn).toContainText(`Unit: ${chosen}`);
    await expect(page).toHaveURL(new RegExp(`/system/log\\?unit=${chosen}`));
    await expect.poll(() => reqs.at(-1)).toContain(`unit=${chosen}`);
    await expect(list).toBeHidden();
    await expect(btn).toBeFocused();
});

test('Escape and an outside click close the dropdown without choosing', async ({page}) => {
    const reqs: string[] = [];
    page.on('request', (r) => {
        if (r.url().includes('/api/system/v1/log?')) reqs.push(new URL(r.url()).search);
    });
    await page.goto('/system/log');
    await expect(page.locator('.ol-line').first()).toBeVisible();
    const n = reqs.length;
    const btn = page.getByRole('combobox', {name: 'Tag'});
    await btn.click();
    const list = page.getByRole('listbox', {name: 'Tag'});
    await expect(list).toBeVisible();
    await page.keyboard.press('ArrowDown');
    await page.keyboard.press('Escape');
    await expect(list).toBeHidden();
    await expect(btn).toBeFocused();
    await expect(btn).toContainText('Tag: all');
    await btn.click();
    await expect(list).toBeVisible();
    await page.locator('h1').click();
    await expect(list).toBeHidden();
    await expect(btn).toContainText('Tag: all');
    expect(reqs.length).toBe(n);
    // keyboard alone from the toolbar: the trigger opens on ↓, Tab closes
    await btn.focus();
    await page.keyboard.press('ArrowDown');
    await expect(list).toBeVisible();
    await page.keyboard.press('Tab');
    await expect(list).toBeHidden();
});

test('the time range: a preset, then a picked from/to pair as epoch seconds', async ({page}) => {
    const reqs: string[] = [];
    page.on('request', (r) => {
        if (r.url().includes('/api/system/v1/log?')) reqs.push(new URL(r.url()).search);
    });
    await page.goto('/system/log');
    await expect(page.locator('.ol-line').first()).toBeVisible();
    const btn = page.getByRole('button', {name: 'Time range'});
    await expect(btn).toContainText('all');
    await btn.click();
    const pop = page.getByRole('dialog', {name: 'Time range'});
    await pop.getByRole('button', {name: '1 h', exact: true}).click();
    await expect.poll(() => reqs.at(-1)).toContain('since=-1h');
    await expect(btn).toContainText('1 h');
    await expect(pop).toBeHidden();
    await btn.click();
    await pop.locator('input[type=datetime-local]').nth(0).fill('2026-09-08T10:00');
    await pop.locator('input[type=datetime-local]').nth(1).fill('2026-09-08T12:30');
    await pop.getByRole('button', {name: 'Apply'}).click();
    await expect.poll(() => reqs.at(-1)).toMatch(/since=%40\d+.*until=%40\d+/);
    const last = reqs.at(-1)!;
    const s = Number(new URLSearchParams(last).get('since')!.slice(1));
    const u = Number(new URLSearchParams(last).get('until')!.slice(1));
    expect(u - s).toBe(150 * 60);
    await expect(btn).toContainText('–');
    await btn.click();
    await page.keyboard.press('Escape');
    await expect(pop).toBeHidden();
});

// task 40: oldest first is the default now (task 31 had newest first); a browser that chose
// keeps its choice
test('oldest first by default, scrolled to the bottom; newest first on request, remembered', async ({page}) => {
    await page.goto('/system/log');
    await expect(page.locator('.ol-line').first()).toBeVisible();
    const times = () => page.locator('.ol-ts').allTextContents();
    const oldest = await times();
    expect(oldest.length).toBeGreaterThan(1);
    expect([...oldest].sort()).toEqual(oldest);
    const order = page.getByLabel('Order');
    await expect(order).toHaveValue('oldest');
    const box = page.locator('.ol-journal');
    await expect.poll(() => box.evaluate((el) => el.scrollHeight - el.scrollTop - el.clientHeight)).toBeLessThan(2);
    // task 178: only the rows in view and a margin are in the DOM, so the rendered rows are
    // compared by their order and their first, not as the whole list
    const newestFirst = async () => {
        const t = await times();
        return t.length > 1 && [...t].sort().reverse().join() === t.join() && t[0] === oldest.at(-1);
    };
    await order.selectOption('newest');
    await expect.poll(newestFirst).toBe(true);
    await page.reload();
    await expect(page.locator('.ol-line').first()).toBeVisible();
    await expect(page.getByLabel('Order')).toHaveValue('newest');
    await expect.poll(newestFirst).toBe(true);
});

// task 44: the journal box fills the window below the filter bar. Its bottom edge is the window's
// whether the log has thirty lines or three hundred, the page itself does not scroll - only the box
// does - and a resize or an opened disclosure moves that edge live. Task 104: edge to edge - the box's
// bottom is the window's bottom and its right edge the window's right edge, no page padding around it.
test('the journal box reaches the bottom of the window and is the only thing that scrolls', async ({page}) => {
    await page.goto('/system/log');
    await expect(page.locator('.ol-line').first()).toBeVisible();
    const box = page.locator('.ol-journal');
    const pad = 0; // task 104: no page padding under the log
    const edge = await box.evaluate((el) => ({right: el.getBoundingClientRect().right, window: document.documentElement.clientWidth}));
    expect(Math.abs(edge.right - edge.window), 'the log reaches the window\'s right edge').toBeLessThanOrEqual(1);
    const bottom = async () => {
        const b = (await box.boundingBox())!;
        return b.y + b.height;
    };
    const height = () => page.evaluate(() => window.innerHeight);
    // the page does not scroll: the document is no taller than the window
    const pageScrolls = () => page.evaluate(() => document.documentElement.scrollHeight > window.innerHeight + 1);
    const boxScrolls = () => box.evaluate((el) => el.scrollHeight > el.clientHeight + 1);
    expect(Math.abs((await bottom()) - ((await height()) - pad))).toBeLessThanOrEqual(1);
    expect(await pageScrolls()).toBe(false);
    await expect(box).toHaveCSS('overflow-y', 'auto');
    // task 93: the settings open over the page in a sheet; the box stays where it is and as tall
    const top = (await box.boundingBox())!.y;
    await page.getByRole('button', {name: 'Log settings'}).click();
    const sheet = page.getByRole('dialog', {name: 'Log settings'});
    await expect(sheet).toBeVisible();
    expect((await box.boundingBox())!.y).toBe(top);
    expect(Math.abs((await bottom()) - ((await height()) - pad))).toBeLessThanOrEqual(1);
    await page.keyboard.press('Escape');
    await expect(sheet).toBeHidden();
    await expect.poll(async () => (await box.boundingBox())!.y).toBe(top);
    // a short window: the box follows the resize, thirty lines no longer fit and the box - and
    // only the box - scrolls
    const vp = page.viewportSize()!;
    await page.setViewportSize({width: vp.width, height: 480});
    await expect.poll(async () => Math.abs((await bottom()) - (480 - pad))).toBeLessThanOrEqual(1);
    expect(await boxScrolls()).toBe(true);
    expect(await pageScrolls()).toBe(false);
    // and back
    await page.setViewportSize(vp);
    await expect.poll(async () => Math.abs((await bottom()) - ((await height()) - pad))).toBeLessThanOrEqual(1);
});

test('a streamed line appends at the bottom and follows only while the view is at the bottom', async ({page, baseURL}) => {
    await page.context().addCookies([{name: 'stub-log-churn', value: '1', url: baseURL!}]);
    await page.goto('/system/log');
    await expect(page.getByText(/churn line/).first()).toBeVisible();
    const box = page.locator('.ol-journal');
    const gap = () => box.evaluate((el) => el.scrollHeight - el.scrollTop - el.clientHeight);
    // following: the newest line is the last one and the view sits on it
    await expect.poll(gap).toBeLessThan(2);
    await expect(page.locator('.ol-line').last()).toContainText(/churn line/);
    // task 44: the box is as tall as the window allows now, so on a desktop the first lines still
    // fit in it - wait until the churn has made it overflow by more than the guard's 40 px, or
    // "the top" and "the bottom" are the same place and there is no history to scroll into
    await expect.poll(() => box.evaluate((el) => el.scrollHeight - el.clientHeight)).toBeGreaterThan(60);
    // scrolled up into the history: more lines arrive (the box grows - task 178: a line beyond
    // the rendered window is a taller spacer, not a row) and the view stays put
    const height = () => box.evaluate((el) => el.scrollHeight);
    await box.evaluate((el) => el.scrollTo({top: 0}));
    const n = await height();
    await expect.poll(height).toBeGreaterThan(n + 20);
    expect(await box.evaluate((el) => el.scrollTop)).toBe(0);
    // back at the bottom: the view follows again
    await box.evaluate((el) => el.scrollTo({top: el.scrollHeight}));
    const m = await height();
    await expect.poll(height).toBeGreaterThan(m);
    await expect.poll(gap).toBeLessThan(2);
});
