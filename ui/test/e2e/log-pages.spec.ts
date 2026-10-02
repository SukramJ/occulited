import {expect, test, type Locator, type Page} from '@playwright/test';

// task 178: the whole log since boot, loaded while scrolling. The stub serves a boot of as many
// entries as the cookie names, in pages of 1000 with a cursor per line; the page keeps them in
// one contiguous run, renders only the rows in view, says at each end what is there, and never
// cuts the log silently.

async function longBoot(page: Page, baseURL: string, entries: number) {
    await page.context().addCookies([{name: 'stub-log-pages', value: String(entries), url: baseURL}]);
}

function box(page: Page): Locator {
    return page.locator('.lg-box');
}

/** the rendered rows' numbers, in DOM order */
async function rowNumbers(page: Page): Promise<number[]> {
    return page.locator('.lg-box .ol-line .ol-msg').evaluateAll((els) => els.map((e) => Number(/Boot line (\d+)/.exec(e.textContent ?? '')?.[1])));
}

async function scrollTo(page: Page, where: 'top' | 'bottom' | number) {
    await box(page).evaluate((el, where) => {
        el.scrollTop = where === 'top' ? 0 : where === 'bottom' ? el.scrollHeight : where;
    }, where);
}

/** the row at the top of the view and its top relative to the box; inView once the rendered rows
 * are those of the current scroll position (a row straddles the box's top edge) */
async function topRow(page: Page): Promise<{text: string; top: number; inView: boolean}> {
    return box(page).evaluate((el) => {
        const rows = [...el.querySelectorAll('.ol-line')] as HTMLElement[];
        const boxTop = el.getBoundingClientRect().top;
        const row = rows.find((r) => r.getBoundingClientRect().bottom > boxTop);
        if (!row) return {text: '', top: NaN, inView: false};
        const top = row.getBoundingClientRect().top - boxTop;
        return {text: row.querySelector('.ol-msg')!.textContent ?? '', top, inView: top <= 0};
    });
}

/** where the row with this message sits relative to the box's top, or null when it is not rendered */
async function topOf(page: Page, text: string): Promise<number | null> {
    return box(page).evaluate((el, text) => {
        const rows = [...el.querySelectorAll('.ol-line')] as HTMLElement[];
        const row = rows.find((r) => r.querySelector('.ol-msg')!.textContent === text);
        return row ? row.getBoundingClientRect().top - el.getBoundingClientRect().top : null;
    }, text);
}

/** the page, with the filter row unfolded on the phone: the count and the jump buttons are in it (task 104).
 * B-292: the fold is looked for once the log is there - asked at once after goto, a loaded runner had
 * not rendered it yet, and the row stayed folded. */
async function open(page: Page, url = '/system/log') {
    await page.goto(url);
    await box(page).waitFor({state: 'attached'});
    const fold = page.locator('.lg-filters-toggle');
    if (await fold.isVisible()) await fold.click();
}

async function count(page: Page): Promise<number> {
    return Number(await page.locator('[data-count]').getAttribute('data-count'));
}

/** scrolls to the old end until the run starts at the log's start */
async function loadAll(page: Page, oldEnd: 'top' | 'bottom' = 'top') {
    for (let i = 0; i < 40; i++) {
        // the run's first line moves with every page (the count stops at the cap). B-292: read
        // before the edge is looked at - a page that lands in between brings the edge with it, so a
        // run that is complete is never taken as one still waiting for its next page
        const first = await page.locator('[data-count]').getAttribute('data-first');
        if ((await page.locator('[data-edge="start"]').count()) > 0) return;
        await scrollTo(page, oldEnd);
        await expect.poll(() => page.locator('[data-count]').getAttribute('data-first'), {timeout: 5000}).not.toBe(first);
    }
    throw new Error('the start of the log never came');
}

test('the tail first, then the pages while scrolling up to the start, contiguous and in place', async ({page, baseURL}) => {
    await longBoot(page, baseURL!, 3500);
    await open(page);
    // the newest 1000 of 3500, the count says so and that more load while scrolling
    await expect(page.locator('[data-count]')).toHaveText('1,000 entries · more load while scrolling');
    await expect(page.locator('[data-edge="more"]')).toHaveText('Older entries load while scrolling.');
    // only the rows in view and a margin are in the DOM
    expect((await rowNumbers(page)).length).toBeLessThan(200);
    // the view stands at the newest line (once the run has been scrolled to its home end)
    await expect.poll(async () => (await rowNumbers(page)).at(-1)).toBe(3499);

    // near the top a page loads, and the row at the top of the view stays where it was.
    // B-292: the older page is held until the row at the top has been read - the scroll's window
    // is rendered a frame later, and a loaded runner answered the page before or after the reading
    // as it happened - and released then, so the reading is always of the view before the page.
    const olderPage = /\/api\/system\/v1\/log\?.*before=/;
    let release!: () => void;
    const held = new Promise<void>((r) => (release = r));
    await page.route(olderPage, async (route) => {
        await held;
        await route.continue();
    });
    await scrollTo(page, 800);
    await expect(page.locator('[data-edge="loading"]')).toBeVisible();
    await expect.poll(async () => (await topRow(page)).inView).toBe(true);
    const before = await topRow(page);
    release();
    await expect.poll(() => count(page)).toBe(2000);
    await page.unroute(olderPage);
    // within a pixel and a half: the rows lie on fractional pixels, while Chromium keeps the scroll
    // position in whole ones, so a pixel of rounding stays (1.19 px at most in 400 phone runs). The
    // whole-pixel row heights before B-292 drifted by 2.95 px, which this still catches.
    await expect.poll(async () => Math.abs(((await topOf(page, before.text)) ?? Infinity) - before.top)).toBeLessThan(1.5);

    // to the start: every page once, the count the whole boot, the first line the boot's first
    await loadAll(page);
    await expect(page.locator('[data-edge="start"]')).toHaveText('Start of the log');
    expect(await count(page)).toBe(3500);
    await expect(page.locator('[data-count]')).toHaveText('3,500 entries');
    await scrollTo(page, 'top');
    await expect.poll(async () => (await rowNumbers(page))[0]).toBe(0);
    // the rows in the DOM are consecutive wherever the view is: no gap, no doubled line
    for (const y of [0, 5000, 23_000, 41_000]) {
        await scrollTo(page, y);
        await page.waitForTimeout(100);
        const nums = await rowNumbers(page);
        expect(nums.length).toBeGreaterThan(10);
        for (let i = 1; i < nums.length; i++) expect(nums[i]).toBe(nums[i - 1]! + 1);
    }
    // the live end is attached: the stream started after the run's last line
    await scrollTo(page, 'bottom');
    await expect.poll(async () => (await rowNumbers(page)).at(-1)).toBe(3499);
});

test('the jump to the start of the boot pages towards the newest, then the stream takes over', async ({page, baseURL}) => {
    await longBoot(page, baseURL!, 2500);
    await open(page);
    await expect(page.locator('[data-count]')).toHaveAttribute('data-count', '1000');
    const stream = page.waitForRequest((r) => r.url().includes('/log/stream') && r.url().includes('after=c2499'));
    await page.getByRole('button', {name: 'Start of the boot'}).click();
    // with every boot on screen the jump goes to this boot first
    await expect(page).toHaveURL(/boot=0/);
    await expect(page.locator('[data-edge="start"]')).toHaveText('Start of the boot');
    await expect.poll(async () => (await rowNumbers(page))[0]).toBe(0);
    await expect(page.locator('[data-count]')).toHaveText('1,000 entries · more load while scrolling');
    // the run is detached from the live end: the newer pages load at the bottom, the button back
    await expect(page.locator('[data-edge="more"]')).toHaveText('Newer entries load while scrolling.');
    const newest = page.getByRole('button', {name: 'Newest'});
    await expect(newest).toBeVisible();
    // B-292: the count is read once per round - read twice, a page that landed in between left the
    // round waiting for a change after the last page
    for (let i = 0; i < 5; i++) {
        const n = await count(page);
        if (n >= 2500) break;
        await scrollTo(page, 'bottom');
        await expect.poll(() => count(page)).not.toBe(n);
    }
    expect(await count(page)).toBe(2500);
    await expect(newest).toHaveCount(0);
    await expect(page.locator('[data-edge="more"]')).toHaveCount(0);
    await stream;
});

test('past 20 000 entries the far end is unloaded and says so; Newest is the tail again', async ({page, baseURL}) => {
    test.setTimeout(120_000);
    await longBoot(page, baseURL!, 21_500);
    await open(page);
    await expect(page.locator('[data-count]')).toHaveAttribute('data-count', '1000');
    await loadAll(page);
    await expect(page.locator('[data-count]')).toHaveText('20,000 entries · more load while scrolling');
    await expect(page.locator('[data-edge="unloaded"]')).toHaveText('Newer entries were unloaded; they load again while scrolling.');
    await scrollTo(page, 'top');
    await expect.poll(async () => (await rowNumbers(page))[0]).toBe(0);
    await page.getByRole('button', {name: 'Newest'}).click();
    await expect(page.locator('[data-count]')).toHaveAttribute('data-count', '1000');
    await expect.poll(async () => (await rowNumbers(page)).at(-1)).toBe(21_499);
});

test('newest first: the old end is at the bottom and pages load there', async ({page, baseURL}) => {
    await longBoot(page, baseURL!, 2500);
    await open(page);
    await expect(page.locator('[data-count]')).toHaveAttribute('data-count', '1000');
    await page.getByLabel('Order').selectOption('newest');
    await expect.poll(async () => (await rowNumbers(page))[0]).toBe(2499);
    await loadAll(page, 'bottom');
    expect(await count(page)).toBe(2500);
    await scrollTo(page, 'bottom');
    await expect.poll(async () => (await rowNumbers(page)).at(-1)).toBe(0);
    await expect(page.locator('[data-edge="start"]')).toHaveText('Start of the log');
});

test('with a time range the end is the range\'s start, and the jump goes there', async ({page, baseURL}) => {
    await longBoot(page, baseURL!, 1500);
    await open(page, '/system/log?since=@1757350800');
    await expect(page.locator('[data-count]')).toHaveAttribute('data-count', '1000');
    await page.getByRole('button', {name: 'Start of the range'}).click();
    await expect(page).not.toHaveURL(/boot=0/);
    await expect(page.locator('[data-edge="start"]')).toHaveText('Start of the time range');
    await expect.poll(async () => (await rowNumbers(page))[0]).toBe(0);
});
