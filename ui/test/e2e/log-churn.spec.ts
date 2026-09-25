import {test, expect, type Page} from '@playwright/test';

// task 104: on a phone the filters' second row is folded behind a Filters toggle; these tests use its
// controls, so they start with the fold open (the page remembers it per browser)
test.beforeEach(async ({page}) => {
    await page.addInitScript(() => localStorage.setItem('ol.log.filters', 'open'));
});

// B-59: the Log page's filters under a live stream on a slow journal. Two cookies tell the stub
// what a busy Pi does: `stub-log-churn` makes the stream emit a line every 200 ms whose tag or
// unit the page has never seen, so the option lists of the two selects would keep moving if they
// followed the lines; `stub-log-delay` makes /log take that long (twice as long without a tag
// filter), so two answers can arrive in the wrong order. A choice made while lines arrive must
// stick: the select still shows it, the request carries it, the rendered lines obey it.
async function busyBox(page: Page, baseURL: string, delay = 0) {
    await page.context().addCookies([
        {name: 'stub-log-churn', value: '1', url: baseURL},
        {name: 'stub-log-delay', value: String(delay), url: baseURL},
    ]);
}

// task 40: the two dropdowns are the shell's own filterable select - a combobox trigger, a
// listbox in the popover; the option list is read with the popover open
async function choose(page: Page, label: string, value: string) {
    await page.getByRole('combobox', {name: label}).click();
    await page.getByRole('listbox', {name: label}).getByRole('option', {name: value, exact: true}).click();
}
async function options(page: Page, label: string): Promise<string[]> {
    await page.getByRole('combobox', {name: label}).click();
    const names = await page.getByRole('listbox', {name: label}).getByRole('option').allTextContents();
    await page.keyboard.press('Escape');
    return names.map((n) => n.trim());
}

// every rendered line's source: the unit is the title, the tag the text before the pid
async function sources(page: Page) {
    return page.locator('.ol-line .ol-src').evaluateAll((els) => els.map((e) => ({unit: e.getAttribute('title') ?? '', tag: (e.textContent ?? '').replace(/\[\d+\]$/, '')})));
}

test('a unit and a tag chosen while lines arrive stay chosen and filter the lines', async ({page, baseURL}) => {
    await busyBox(page, baseURL!, 800);
    const reqs: string[] = [];
    page.on('request', (r) => {
        if (r.url().includes('/api/system/v1/log?')) reqs.push(new URL(r.url()).search);
    });
    await page.goto('/system/log');
    await expect(page.locator('.ol-line').first()).toBeVisible();
    // the stream is up: a churn line has arrived
    await expect(page.getByText(/churn line/).first()).toBeVisible();
    const unitSel = page.getByRole('combobox', {name: 'Unit'});
    const tagSel = page.getByRole('combobox', {name: 'Tag'});
    // hmipserver's lines carry two tags; the unit is chosen, and the tag right after it, while
    // the unit's own (slower) answer is still on its way
    await choose(page, 'Unit', 'hmipserver');
    await expect.poll(() => reqs.at(-1)).toContain('unit=hmipserver');
    await page.waitForTimeout(300);
    await expect(unitSel).toContainText('Unit: hmipserver');
    // the tag hmipserver is shown by its name, HmIP server; the request carries the identifier
    await choose(page, 'Tag', 'HmIP server');
    await expect.poll(() => reqs.at(-1)).toContain('tag=hmipserver');
    // both answers are in by now, and lines kept arriving
    await page.waitForTimeout(2500);
    await expect(tagSel).toContainText('Tag: HmIP server');
    await expect(unitSel).toContainText('Unit: hmipserver');
    const last = new URLSearchParams(reqs.at(-1));
    expect(last.get('unit')).toBe('hmipserver');
    expect(last.get('tag')).toBe('hmipserver');
    const src = await sources(page);
    expect(src.length).toBeGreaterThan(0);
    for (const s of src) expect(s).toEqual({unit: 'hmipserver', tag: 'hmipserver'});
    // the option lists did not lose the choice, nor the other tag of the unit
    const tags = await options(page, 'Tag');
    expect(tags.filter((x) => x === 'HmIP server')).toHaveLength(1);
    expect(tags.filter((x) => x === 'java')).toHaveLength(1);
    expect((await options(page, 'Unit')).filter((x) => x === 'hmipserver')).toHaveLength(1);
});

test('the option lists grow on a reload, not with every line', async ({page, baseURL}) => {
    await busyBox(page, baseURL!);
    await page.goto('/system/log');
    await expect(page.getByText(/churn line/).first()).toBeVisible();
    const n = (await options(page, 'Tag')).length;
    // a second of churn: fresh tags arrived in the lines...
    await page.waitForTimeout(1200);
    await expect(page.locator('.ol-line', {hasText: /churn line/}).nth(2)).toBeVisible();
    // ...and the list is where it was
    expect((await options(page, 'Tag')).length).toBe(n);
    // a reload takes them in
    await page.getByRole('button', {name: 'Refresh'}).click();
    await expect.poll(async () => (await options(page, 'Tag')).length).toBeGreaterThan(n);
});

// task 93: the row above the filters is the head row - the view switch, the boot, the download, the gear
test('the head row has room above the filter row', async ({page}) => {
    await page.goto('/system/log');
    await expect(page.locator('.ol-line').first()).toBeVisible();
    await expect(page.getByRole('button', {name: /^Boot: /})).toBeVisible();
    const head = page.locator('.lg-head');
    const toolbar = page.locator('.ol-toolbar').first();
    const hb = (await head.boundingBox())!;
    const tb = (await toolbar.boundingBox())!;
    expect(tb.y - (hb.y + hb.height)).toBeGreaterThanOrEqual(8);
});
