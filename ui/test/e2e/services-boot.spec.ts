import {expect, test, type Locator, type Page} from '@playwright/test';
import {readFileSync} from 'node:fs';
import {scrollTo, scrollY} from './scroll';

// Task 93: the boot order graph on the Services page, below the timers (the maintainer's layout of
// 2026-09-12 evening). The stub's GET /boot is the Charly's boot of 2026-09-12 (209 units), read
// from the capture by internal/bootchart - its figures are systemd-analyze's.

const BOOT = '4c1d2a6b0e8f4a2b9c3d5e7f8a1b2c3d';
const PREV = '7a8b9c0d1e2f3a4b5c6d7e8f9a0b1c2d';

/**
 * A tap on the phone, a click elsewhere. The controls were driven from the keyboard while the
 * Services page was wider than a phone (B-105): the phone project zoomed it out, and the timers the
 * timers spec adds meanwhile changed that zoom under a tap. The page fits the phone now.
 */
async function press(_page: Page, target: Locator) {
    if (test.info().project.use.isMobile) await target.tap();
    else await target.click();
}

async function openTimeline(page: Page) {
    await page.goto('/system/services');
    const toggle = page.getByRole('button', {name: 'Boot timeline'});
    await expect(toggle).toHaveAttribute('aria-expanded', 'false');
    await press(page, toggle);
    await expect(toggle).toHaveAttribute('aria-expanded', 'true');
    const section = page.locator('section#boot');
    await expect(section.locator('[data-figure="total"] .v')).toBeVisible();
    return section;
}

test('closed until opened, then read once: the figures, below the timers', async ({page}) => {
    const asked: string[] = [];
    page.on('request', (r) => {
        if (r.url().includes('/api/system/v1/boot')) asked.push(new URL(r.url()).pathname);
    });
    await page.goto('/system/services');
    await expect(page.getByRole('heading', {name: 'Timers'})).toBeVisible();
    await expect(page.getByRole('button', {name: 'Boot timeline'})).toBeVisible();
    expect(asked.filter((p) => p === '/api/system/v1/boot')).toEqual([]);
    const section = await openTimeline(page);
    expect(asked.filter((p) => p === '/api/system/v1/boot')).toHaveLength(1);
    // below the timers
    const timers = (await page.getByRole('heading', {name: 'Timers'}).boundingBox())!;
    expect((await section.boundingBox())!.y).toBeGreaterThan(timers.y);
    await expect(section.locator('[data-figure="kernel"] .v')).toHaveText('8.71 s');
    await expect(section.locator('[data-figure="userspace"] .v')).toHaveText('1 min 46 s');
    await expect(section.locator('[data-figure="total"] .v')).toHaveText('1 min 55 s');
    await expect(section.locator('[data-figure="slowest"]')).toContainText('hmipserver.service');
    await expect(section.locator('[data-figure="slowest"]')).toContainText('42.3 s');
    // opened stays opened for this browser
    await page.reload();
    await expect(page.getByRole('button', {name: 'Boot timeline'})).toHaveAttribute('aria-expanded', 'true');
});

test('the chart: a bar per unit shown, the 10 ms filter, only services, the chain, the zoom', async ({page}, info) => {
    test.skip(!!info.project.use.isMobile, 'the list is the phone default; the chart has its own desktop test');
    const section = await openTimeline(page);
    await expect(section.getByLabel('View')).toHaveValue('chart');
    const rows = section.locator('g.bt-row');
    const names = section.locator('.bt-name');
    const count = async () => Number((await section.locator('.bt-count').textContent())!.match(/^\d+/)![0]);
    await expect(rows.first()).toBeVisible();
    const short = await rows.count();
    expect(short).toBe(await names.count());
    expect(short).toBe(await count());
    // the targets and devices under 10 ms are hidden by default
    await expect(section.locator('g.bt-row[data-unit="sysinit.target"]')).toHaveCount(0);
    await section.getByRole('checkbox', {name: 'Hide units under 10 ms'}).uncheck();
    await expect.poll(() => rows.count()).toBe(209);
    await section.getByRole('checkbox', {name: 'Only services'}).check();
    await expect.poll(async () => (await section.locator('g.bt-row').evaluateAll((gs) => gs.every((g) => g.getAttribute('data-unit')!.endsWith('.service'))))).toBe(true);
    await section.getByRole('checkbox', {name: 'Only services'}).uncheck();
    await section.getByRole('checkbox', {name: 'Hide units under 10 ms'}).check();
    await expect.poll(() => rows.count()).toBe(short);

    // the critical chain: its units coloured and all there, even the short ones; the rest dimmed
    const chainButton = section.getByRole('button', {name: 'Critical chain'});
    await chainButton.click();
    await expect(chainButton).toHaveAttribute('aria-pressed', 'true');
    await expect(section.locator('g.bt-row.chain')).toHaveCount(32);
    await expect(section.locator('g.bt-row.chain[data-unit="sysinit.target"]')).toHaveCount(1);
    expect(await section.locator('g.bt-row.dim').count()).toBeGreaterThan(0);
    const chainFill = await section.locator('g.bt-row.chain .bt-span').first().evaluate((e) => getComputedStyle(e).fill);
    const plainFill = await section.locator('g.bt-row.dim .bt-span').first().evaluate((e) => getComputedStyle(e).fill);
    expect(chainFill).not.toBe(plainFill);

    // fit by default, then Ctrl+wheel and the buttons zoom. Task 113: the bars scroll sideways in
    // `.bt-plot`; the names keep a column of their own beside it.
    const bars = section.locator('svg.bt-bars');
    const width = async () => Number(await bars.getAttribute('width'));
    const fit = await width();
    // The wheel has to land on the chart: where the clicks above left the page scrolled, the chart could
    // start below the window's edge (task 98's opening motion shifted that under load, and the wheel
    // went to y 822 of a 720 px window). Task 113 made the plot as tall as its 209 units, so its own
    // box says nothing about where the *window* shows it; the axis row does - it is sticky at the top
    // of the page's scrolling box - and just under it is the plot.
    await section.locator('.bt-axisrow').scrollIntoViewIfNeeded();
    const axis = (await section.locator('.bt-axisrow').boundingBox())!;
    const box = (await section.locator('.bt-plot').boundingBox())!;
    expect(fit).toBeLessThanOrEqual(box.width);
    await page.mouse.move(box.x + box.width / 2, axis.y + axis.height + 40);
    await page.keyboard.down('Control');
    await page.mouse.wheel(0, -200);
    await page.keyboard.up('Control');
    await expect.poll(width).toBeGreaterThan(fit);
    await section.getByRole('button', {name: 'Fit to width'}).click();
    await expect.poll(width).toBe(fit);
    await section.getByRole('button', {name: 'Zoom in'}).click();
    await expect.poll(width).toBeGreaterThan(fit);
    // the axis follows the bars sideways, and the names do not move with them
    const namesX = (await section.locator('.bt-names').boundingBox())!.x;
    await section.locator('.bt-plot').evaluate((el) => el.scrollTo({left: 400}));
    await expect.poll(() => section.locator('.bt-axisstrip').evaluate((el) => el.scrollLeft)).toBe(400);
    expect((await section.locator('.bt-names').boundingBox())!.x).toBe(namesX);
});

// Task 113: "startup graph not in a panel with own scrollers. instead it should just grow the page and
// the user scrolls the whole page". Nothing in the section scrolls downwards; the page grows with the
// units and the axis stays under the top bar while it is read.
test('the section has no scroll area of its own: the page grows, and the axis stays under the bar', async ({page}, info) => {
    test.skip(!!info.project.use.isMobile, 'the chart is the desktop view; the list grows the same way');
    await page.setViewportSize({width: 1280, height: 700});
    const section = await openTimeline(page);
    await expect(section.locator('g.bt-row').first()).toBeVisible();

    // nothing inside the section is a vertical scroller: `auto` and `scroll` are what puts a second
    // scroll bar inside the page, and a used `overflow-x: auto` turns a `visible` overflow-y into
    // `auto` - which is exactly how the chart and the list would grow one back
    const vertical = () =>
        section.evaluate((el) =>
            [el, ...Array.from(el.querySelectorAll('*'))]
                .filter((e) => ['auto', 'scroll'].includes(getComputedStyle(e).overflowY))
                .map((e) => `${e.tagName.toLowerCase()}.${String(e.getAttribute('class') ?? '').trim().replace(/\s+/g, '.')}`),
        );
    expect(await vertical()).toEqual([]);

    // the page itself grows with the units: the 10 ms filter off adds all 209 of them
    const pageHeight = () => page.locator('.ol-scrollport').evaluate((el) => el.scrollHeight);
    const short = await pageHeight();
    await section.getByRole('checkbox', {name: 'Hide units under 10 ms'}).uncheck();
    await expect.poll(() => section.locator('g.bt-row').count()).toBe(209);
    expect(await pageHeight(), 'the page is longer with every unit shown').toBeGreaterThan(short);
    expect(await vertical(), 'and still nothing inside scrolls').toEqual([]);

    // scrolled deep into the units, the axis is still on screen, directly under the top bar
    await scrollTo(page, (await pageHeight()) / 2);
    await expect.poll(() => scrollY(page)).toBeGreaterThan(200);
    const axis = (await section.locator('.bt-axisrow').boundingBox())!;
    const port = (await page.locator('.ol-scrollport').boundingBox())!;
    expect(Math.abs(axis.y - port.y), 'the axis sits at the top of the scrolling box').toBeLessThanOrEqual(1);
    await expect(section.locator('.bt-axisrow')).toBeInViewport({ratio: 1});

    // the 10 ms filter is on again by default for the next reader
    await section.getByRole('checkbox', {name: 'Hide units under 10 ms'}).check();
    await page.reload();
    await expect(page.getByRole('checkbox', {name: 'Hide units under 10 ms'})).toBeChecked();
});

test('a unit: its row above highlighted and in view, its panel, its log of this boot', async ({page}) => {
    const section = await openTimeline(page);
    await section.getByLabel('View').selectOption('list');
    await press(page, section.locator('table.bt-list').getByRole('button', {name: 'hmipserver.service', exact: true}));
    const row = page.locator('tr[data-service="hmipserver"]');
    await expect(row).toHaveClass(/ol-bt-highlight/);
    await expect(row).toBeInViewport();
    const panel = page.locator('aside.bt-panel');
    await expect(panel.getByRole('heading', {name: 'hmipserver.service'})).toBeVisible();
    await expect(panel).toContainText('42.3 s');
    await expect(panel).toContainText('In the critical chain');
    await expect(panel.getByRole('heading', {name: 'Waited for'})).toBeVisible();
    await expect(panel).toBeInViewport();
    // another unit from the panel moves the highlight; a target has no row
    await press(page, panel.getByRole('button', {name: 'multimacd.service'}));
    await expect(page.locator('tr[data-service="multimacd"]')).toHaveCount(0);
    await expect(row).not.toHaveClass(/ol-bt-highlight/);
    await expect(panel.getByRole('heading', {name: 'multimacd.service'})).toBeVisible();
    await expect(panel).toContainText('Not in the tables above: a filter hides it.');
    // Escape closes it
    await page.keyboard.press('Escape');
    await expect(panel).toHaveCount(0);

    await press(page, section.locator('table.bt-list').getByRole('button', {name: 'rfd.service', exact: true}));
    const log = page.locator('aside.bt-panel').getByRole('link', {name: 'Log of this unit in this boot'});
    await expect(log).toHaveAttribute('href', `/system/log?unit=rfd&boot=${BOOT}`);
    await press(page, log);
    await expect(page).toHaveURL(new RegExp(`/system/log\\?unit=rfd&boot=${BOOT}$`));
    // task 104: on a phone the Log page's second filter row is folded; its toggle counts the unit, and opens it
    await expect(page.locator('.lg-head')).toBeVisible();
    const filters = page.locator('.lg-filters-toggle');
    if ((await filters.count()) > 0) {
        await expect(filters.locator('[data-filter-count]')).toHaveText('1');
        await press(page, filters);
    }
    await expect(page.getByRole('combobox', {name: 'Unit'})).toContainText('Unit: rfd');
    await expect(page.getByRole('button', {name: /^Boot: /})).toContainText('this boot');
});

test('the list: the phone default, sortable, and the exports', async ({page}, info) => {
    const section = await openTimeline(page);
    const view = section.getByLabel('View');
    await expect(view).toHaveValue(info.project.use.isMobile ? 'list' : 'chart');
    await view.selectOption('list');
    const firstUnit = () => section.locator('table.bt-list tbody tr').first().getAttribute('data-unit');
    // in the order they began, then by duration, the slowest first
    const began = await section.locator('table.bt-list tbody tr td:first-child').allTextContents();
    const secs = began.map((s) => Number(s.replace(/[^\d.]/g, '')));
    expect([...secs].sort((a, b) => a - b)).toEqual(secs);
    await press(page, section.getByRole('button', {name: 'Took'}));
    await expect.poll(firstUnit).toBe('hmipserver.service');
    await expect(section.locator('th[aria-sort="descending"]')).toContainText('Took');
    await press(page, section.getByRole('button', {name: /^Took/}));
    await expect(section.locator('th[aria-sort="ascending"]')).toContainText('Took');

    // the chart as SVG and the data as JSON, named by the boot
    await press(page, section.getByRole('button', {name: 'Export'}));
    const [svg] = await Promise.all([page.waitForEvent('download'), press(page, section.getByRole('menuitem', {name: 'Chart as SVG'}))]);
    expect(svg.suggestedFilename()).toMatch(/^boot-4c1d2a6b-\d{4}-\d\d-\d\d\.svg$/);
    const body = readFileSync((await svg.path())!, 'utf8');
    expect(body.startsWith('<svg xmlns="http://www.w3.org/2000/svg"')).toBe(true);
    expect(body).toContain('hmipserver.service');
    expect(body).not.toContain('var(--');
    await press(page, section.getByRole('button', {name: 'Export'}));
    const [json] = await Promise.all([page.waitForEvent('download'), press(page, section.getByRole('menuitem', {name: 'Data as JSON'}))]);
    expect(json.suggestedFilename()).toMatch(/^boot-4c1d2a6b-\d{4}-\d\d-\d\d\.json$/);
    const parsed = JSON.parse(readFileSync((await json.path())!, 'utf8')) as {boot_id: string; units: unknown[]};
    expect(parsed.boot_id).toBe(BOOT);
    expect(parsed.units).toHaveLength(209);
});

test('/services#boot opens the section and brings it into view; German; nothing in it wider than the page', async ({page}) => {
    await page.addInitScript(() => localStorage.setItem('ol.language', 'de'));
    await page.goto('/system/services#boot');
    const toggle = page.getByRole('button', {name: 'Startablauf'});
    await expect(toggle).toHaveAttribute('aria-expanded', 'true');
    const section = page.locator('section#boot');
    await expect(section.locator('[data-figure="total"]')).toContainText('Start abgeschlossen');
    await expect(section.getByRole('button', {name: 'Kritische Kette'})).toBeVisible();
    await expect(section.getByRole('heading', {name: 'Startablauf'})).toBeInViewport();
    // the chart and the list scroll inside their own boxes; the section itself never runs wide
    expect(await section.evaluate((el) => el.scrollWidth <= el.clientWidth + 1)).toBe(true);
    for (const view of ['list', 'chart']) {
        await section.getByLabel('Ansicht').selectOption(view);
        expect(await section.evaluate((el) => el.scrollWidth <= el.clientWidth + 1)).toBe(true);
    }
});

test('an earlier boot the box kept, and the comparison with the previous boot', async ({page}, info) => {
    const asked: string[] = [];
    page.on('request', (r) => {
        const url = new URL(r.url());
        if (url.pathname === '/api/system/v1/boot') asked.push(url.search);
    });
    const section = await openTimeline(page);
    await section.getByLabel('View').selectOption('list');
    // the comparison: a delta beside each unit the previous boot started too, a dash for the rest,
    // and the change of the figures
    const compareButton = section.getByRole('button', {name: 'Compare with the previous boot'});
    await expect(compareButton).toBeEnabled();
    await press(page, compareButton);
    await expect(compareButton).toHaveAttribute('aria-pressed', 'true');
    expect(asked).toContain(`?id=${PREV}`);
    await expect(section.locator('table.bt-list th', {hasText: 'Δ took'})).toBeVisible();
    const hmip = section.locator('table.bt-list tr[data-unit="hmipserver.service"] td.bt-delta');
    await expect(hmip.nth(1)).toHaveText('+12.0 s');
    await expect(hmip.nth(1)).toHaveClass(/worse/);
    await expect(section.locator('table.bt-list tr[data-unit="addon-redmatic.service"] td.bt-delta').nth(1)).toHaveText('—');
    await expect(section.locator('[data-figure="total"] .bt-deltachip')).toHaveText('+12.0 s');
    if (!info.project.use.isMobile) {
        // the previous boot's bars as outlines in the chart
        await section.getByLabel('View').selectOption('chart');
        await expect(section.locator('g.bt-row[data-unit="hmipserver.service"] rect.bt-ghost')).toHaveCount(1);
        await section.getByLabel('View').selectOption('list');
    }
    // an earlier boot: the kept boots in the selector, that boot's timeline, marked as kept
    const pick = section.getByRole('combobox', {name: 'Boot', exact: true});
    await expect(pick.locator('option')).toHaveCount(3);
    await pick.selectOption(PREV);
    await expect.poll(() => asked.at(-1)).toBe(`?id=${PREV}`);
    await expect(section.locator('.bt-when .ol-badge')).toHaveText('kept');
    await expect(compareButton).toHaveAttribute('aria-pressed', 'false');
    await expect(section.locator('[data-figure="total"] .v')).toHaveText('1 min 43 s');
    await expect(section.locator('table.bt-list tr[data-unit="addon-redmatic.service"]')).toHaveCount(0);
    // its unit's log is that boot's
    await press(page, section.locator('table.bt-list').getByRole('button', {name: 'rfd.service', exact: true}));
    await expect(page.locator('aside.bt-panel').getByRole('link', {name: 'Log of this unit in this boot'})).toHaveAttribute('href', `/system/log?unit=rfd&boot=${PREV}`);
});

// B-153: the running boot after rfd, hmipserver and occulited were restarted - the boot's bars, each
// marked restarted with its state of now; without a kept snapshot the note names the lost units
test('units restarted since the boot keep their bars and are marked; lost ones are named', async ({page, baseURL}) => {
    await page.context().addCookies([{name: 'stub-boot-restarted', value: '1', url: baseURL!}]);
    const section = await openTimeline(page);
    await section.getByLabel('View').selectOption('list');
    const row = (id: string) => section.locator(`table.bt-list tr[data-unit="${id}"]`);
    for (const id of ['rfd.service', 'hmipserver.service']) {
        await expect(row(id).locator('[data-restarted]')).toHaveText(/^restarted \d\d:\d\d$/);
    }
    await expect(row('hmipserver.service')).toContainText('activating');
    await expect(row('sshd.service').locator('[data-restarted]')).toHaveCount(0);
    await expect(section.locator('[data-later]')).toHaveText('Started after the boot, not shown: fstrim.service.');

    await page.context().clearCookies({name: 'stub-boot-restarted'});
    await page.context().addCookies([{name: 'stub-boot-lost', value: '1', url: baseURL!}]);
    // the section stays open across the reload (remembered per browser)
    await page.reload();
    const again = page.locator('section#boot');
    await expect(again.locator('[data-later]')).toHaveText('Started after the boot, not shown: hmipserver.service, occulited.service, rfd.service.');
    await expect(again.locator('tr[data-unit="rfd.service"]')).toHaveCount(0);
});
