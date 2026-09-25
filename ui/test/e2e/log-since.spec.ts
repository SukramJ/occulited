import {expect, test, type Page} from '@playwright/test';

// task 104: on a phone the filters' second row is folded behind a Filters toggle; these tests use its
// controls, so they start with the fold open (the page remembers it per browser)
test.beforeEach(async ({page}) => {
    await page.addInitScript(() => localStorage.setItem('ol.log.filters', 'open'));
});

// The Status page's unclean-shutdown warning opens the Log on the boot before this one, at its end,
// where the box went down (task 93: "the Journal view of the previous boot's end"): /log?boot=-1.
// In RAM the journal holds this boot alone, so occulited keeps /log?since=@<epoch seconds>, an hour
// before the marker, and the page says that earlier boots need a persistent journal. The Log page
// takes the start of its time range from the URL: the first request carries it, and the time range
// button names it instead of "all". The stub's journal holds three boots; stub-journal-ram=1 is a
// journal in RAM.

const PREV = '7a8b9c0d1e2f3a4b5c6d7e8f9a0b1c2d';

function logRequests(page: Page): URLSearchParams[] {
    const reqs: URLSearchParams[] = [];
    page.on('request', (r) => {
        if (r.url().includes('/api/system/v1/log?')) reqs.push(new URL(r.url()).searchParams);
    });
    return reqs;
}

test('the Log page reads since from its URL', async ({page}) => {
    const since = Math.floor(Date.now() / 1000) - 3 * 3600;
    const reqs = logRequests(page);
    await page.goto(`/system/log?since=@${since}`);
    await expect(page.locator('.ol-line').first()).toBeVisible();
    expect(reqs[0]!.get('since')).toBe(`@${since}`);
    await expect(page.getByRole('button', {name: 'Time range'})).toContainText('– now');
    await expect(page.getByRole('button', {name: 'Time range'})).not.toContainText('all');
    // a persistent journal holds the earlier boots: nothing to explain
    await expect(page.getByRole('button', {name: /^Boot: /})).toBeVisible();
    await expect(page.locator('[data-notice="earlier-boots"]')).toHaveCount(0);
});

test('the unclean-shutdown warning opens the previous boot, at its end', async ({page, context, baseURL}) => {
    await context.addCookies([{name: 'stub-warn', value: '1', url: baseURL!}]);
    const reqs = logRequests(page);
    await page.goto('/');
    const link = page.locator('[data-notice="unclean"]').getByRole('link', {name: 'Log', exact: true});
    await expect(link).toHaveAttribute('href', '/log?boot=-1');
    await link.click();
    await expect(page).toHaveURL(/\/system\/log\?boot=-1$/);
    await expect.poll(() => reqs[0]?.get('boot')).toBe('-1');
    expect(reqs[0]!.get('since')).toBeNull();
    // that boot's lines, named in the boot menu, with nothing to follow
    await expect(page.locator('.ol-line').first()).toContainText('Earlier boot line');
    const button = page.getByRole('button', {name: /^Boot: /});
    await expect(button).not.toContainText('all boots');
    await expect(button).not.toContainText('this boot');
    await expect(page.getByRole('checkbox', {name: 'Follow'})).toBeDisabled();
    // at its end: oldest first is the default, so the view stands at the bottom, on its last line
    await expect(page.locator('.ol-line').last()).toBeInViewport();
    await expect.poll(() => page.locator('.lg-box').evaluate((b) => b.scrollHeight - b.scrollTop - b.clientHeight)).toBeLessThanOrEqual(2);
    await expect(page.locator('[data-notice="earlier-boots"]')).toHaveCount(0);
    await button.click();
    await expect(page.getByRole('menu', {name: 'Boot'}).locator(`[data-boot="${PREV}"]`)).toHaveAttribute('aria-checked', 'true');
});

test('in RAM the warning opens this boot from an hour before, and says earlier boots need a persistent journal', async ({page, context, baseURL}) => {
    await context.addCookies([
        {name: 'stub-warn', value: '1', url: baseURL!},
        {name: 'stub-journal-ram', value: '1', url: baseURL!},
    ]);
    const reqs = logRequests(page);
    await page.goto('/');
    await page.locator('[data-notice="unclean"]').getByRole('link', {name: 'Log', exact: true}).click();
    await expect(page).toHaveURL(/\/system\/log\?since=@\d+$/);
    const since = new URL(page.url()).searchParams.get('since');
    await expect.poll(() => reqs[0]?.get('since')).toBe(since);
    expect(reqs[0]!.get('boot')).toBeNull();
    await expect(page.getByRole('button', {name: 'Time range'})).toContainText('– now');
    const note = page.locator('[data-notice="earlier-boots"]');
    await expect(note).toContainText('Earlier boots need a persistent journal.');
    await note.getByRole('button', {name: 'Storage settings'}).click();
    const sheet = page.getByRole('dialog', {name: 'Log settings'});
    await expect(sheet).toBeVisible();
    await expect(sheet.getByRole('tab', {name: 'Journal'})).toHaveAttribute('aria-selected', 'true');
});

test('the RAM note in German', async ({page, context, baseURL}) => {
    await context.addCookies([{name: 'stub-journal-ram', value: '1', url: baseURL!}]);
    await page.addInitScript(() => localStorage.setItem('ol.language', 'de'));
    await page.goto(`/system/log?since=@${Math.floor(Date.now() / 1000) - 3600}`);
    await expect(page.locator('[data-notice="earlier-boots"]')).toContainText('Frühere Systemstarts brauchen ein dauerhaftes Journal.');
});
