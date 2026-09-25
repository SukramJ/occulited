import {expect, test, type Page} from '@playwright/test';
import {pageWidth} from './scroll';

// task 104: on a phone the filters' second row is folded behind a Filters toggle; these tests use its
// controls, so they start with the fold open (the page remembers it per browser)
test.beforeEach(async ({page}) => {
    await page.addInitScript(() => localStorage.setItem('ol.log.filters', 'open'));
});

// Task 93: the kernel's lines in the Log page's viewer - a Source among the filters (all, system,
// kernel) in the URL, dmesg's stamps and the warnings chip for the kernel - the boot menu above the
// viewer, and the settings sheet behind the gear. The stub's journal holds three boots (this one and
// two earlier ones); stub-journal-ram=1 is a journal in RAM with this boot alone.

const PREV = '7a8b9c0d1e2f3a4b5c6d7e8f9a0b1c2d';

function logRequests(page: Page): URLSearchParams[] {
    const reqs: URLSearchParams[] = [];
    page.on('request', (r) => {
        if (r.url().includes('/api/system/v1/log?')) reqs.push(new URL(r.url()).searchParams);
    });
    return reqs;
}
const bootButton = (page: Page) => page.getByRole('button', {name: /^Boot: /});

test('the source: every message, the system, the kernel - in the URL, Back, Forward and a reload', async ({page}) => {
    const reqs = logRequests(page);
    await page.goto('/system/log?unit=rfd');
    await expect(page.locator('.ol-line').first()).toContainText('Sample log line');
    const pick = page.getByLabel('Source');
    await expect(pick).toHaveValue('all');
    await expect(pick.locator('option')).toHaveText(['Source: all', 'Source: system', 'Source: kernel']);
    expect(reqs.at(-1)!.has('kernel')).toBe(false);
    // the kernel's things are not there for the system's lines
    await expect(page.getByRole('button', {name: 'Warnings and errors only'})).toHaveCount(0);
    await expect(page.getByLabel('Timestamps')).toHaveCount(0);

    // the system: kernel=0, and the unit stays
    await pick.selectOption('system');
    await expect(page).toHaveURL(/\/system\/log\?source=system&unit=rfd$/);
    await expect.poll(() => reqs.at(-1)?.get('kernel')).toBe('0');
    expect(reqs.at(-1)!.get('unit')).toBe('rfd');
    await expect(page.getByRole('combobox', {name: 'Unit'})).toContainText('Unit: rfd');

    // the kernel: kernel=1, no unit, no tag, and its stamps and chip
    await pick.selectOption('kernel');
    await expect(page).toHaveURL(/\/system\/log\?source=kernel$/);
    await expect.poll(() => reqs.at(-1)?.get('kernel')).toBe('1');
    expect(reqs.at(-1)!.has('unit')).toBe(false);
    await expect(page.locator('.ol-line').first()).toContainText('Linux version');
    await expect(page.getByRole('combobox', {name: 'Tag'})).toHaveCount(0);
    await expect(page.getByRole('combobox', {name: 'Unit'})).toHaveCount(0);
    await expect(page.getByRole('button', {name: 'Warnings and errors only'})).toBeVisible();
    await expect(page.getByLabel('Timestamps')).toBeVisible();

    await page.goBack();
    await expect(page).toHaveURL(/\/system\/log\?source=system&unit=rfd$/);
    await expect(page.getByLabel('Source')).toHaveValue('system');
    await expect(page.locator('.ol-line').first()).toContainText('Sample log line');
    await expect(page.getByRole('combobox', {name: 'Tag'})).toBeVisible();
    await page.goBack();
    await expect(page).toHaveURL(/\/system\/log\?unit=rfd$/);
    await expect(page.getByLabel('Source')).toHaveValue('all');
    await page.goForward();
    await page.goForward();
    await expect(page).toHaveURL(/\/system\/log\?source=kernel$/);
    await expect(page.locator('.ol-line').first()).toContainText('Linux version');
    await page.reload();
    await expect(page.getByLabel('Source')).toHaveValue('kernel');
    await expect(page.locator('.ol-line').first()).toContainText('Linux version');
});

test('the kernel: since-boot stamps or the wall clock, the warnings chip, the download', async ({page}) => {
    const reqs = logRequests(page);
    await page.goto('/system/log?source=kernel');
    const stamps = page.locator('.ol-line .ol-ts');
    await expect(stamps.first()).toBeVisible();
    expect(await stamps.first().textContent()).toBe('[    0.000000]');
    expect(await page.locator('.ol-line', {hasText: 'mmc0: timeout'}).locator('.ol-ts').textContent()).toBe('[   12.500001]');
    // the stamp keeps dmesg's padding on screen
    await expect(stamps.first()).toHaveCSS('white-space', 'pre');

    const clock = page.getByLabel('Timestamps');
    await expect(clock).toHaveValue('boot');
    await clock.selectOption('wall');
    await expect(stamps.first()).not.toContainText('[');
    // remembered by the browser
    await page.reload();
    await expect(page.getByLabel('Timestamps')).toHaveValue('wall');
    await page.getByLabel('Timestamps').selectOption('boot');
    await expect(stamps.first()).toContainText('[');

    // most of it is noise: one press for the warnings and errors, err and above stand out
    const chip = page.getByRole('button', {name: 'Warnings and errors only'});
    await expect(chip).toHaveAttribute('aria-pressed', 'false');
    await chip.click();
    await expect(chip).toHaveAttribute('aria-pressed', 'true');
    await expect.poll(() => reqs.at(-1)?.get('severity')).toBe('warning');
    await expect(page.getByLabel('Severity')).toHaveValue('warning');
    await expect(page.locator('.ol-line')).toHaveCount(3);
    expect(await page.locator('.ol-line.sev-err').evaluate((e) => getComputedStyle(e).backgroundColor)).not.toBe('rgba(0, 0, 0, 0)');
    expect(await page.locator('.ol-line.sev-warning').evaluate((e) => getComputedStyle(e).backgroundColor)).toBe('rgba(0, 0, 0, 0)');
    await chip.click();
    await expect.poll(() => reqs.at(-1)?.has('severity')).toBe(false);
    await expect(page.locator('.ol-line')).toHaveCount(9);

    // the download is the kernel's lines too
    await page.getByRole('button', {name: 'Download'}).click();
    const href = await page.getByRole('menu', {name: 'Download'}).locator('a[data-format="text"]').getAttribute('href');
    const p = new URL(href!, 'http://box').searchParams;
    expect(p.get('kernel')).toBe('1');
    expect(p.has('unit') || p.has('tag')).toBe(false);
});

test('the boot menu: every boot, an earlier one narrows the lines and has nothing to follow', async ({page}) => {
    const reqs = logRequests(page);
    await page.goto('/system/log?unit=rfd');
    await expect(page.locator('.ol-line').first()).toBeVisible();
    const button = bootButton(page);
    await expect(button).toContainText('Boot: all boots');
    await expect(page.getByRole('checkbox', {name: 'Follow'})).toBeEnabled();
    await button.click();
    const menu = page.getByRole('menu', {name: 'Boot'});
    const items = menu.getByRole('menuitemradio');
    await expect(items).toHaveCount(4);
    // a boot only kept for the Services page's timeline has no lines here
    await expect(menu.locator('[data-boot="9f8e7d6c5b4a39281706f5e4d3c2b1a0"]')).toHaveCount(0);
    await expect(items.nth(0)).toHaveText('All boots');
    await expect(items.nth(0)).toHaveAttribute('aria-checked', 'true');
    await expect(items.nth(1)).toContainText('This boot');
    await expect(items.nth(1)).toContainText('since');
    await expect(items.nth(2)).toContainText('20 h');
    await expect(items.nth(3)).toContainText('1 d 5 h');
    // nothing about a RAM journal where the journal is persistent
    await expect(menu).not.toContainText('persistent journal');
    const box = (await menu.boundingBox())!;
    expect(box.x + box.width).toBeLessThanOrEqual(page.viewportSize()!.width);

    await items.nth(2).click();
    await expect(page).toHaveURL(new RegExp(`/system/log\\?unit=rfd&boot=${PREV}$`));
    await expect.poll(() => reqs.at(-1)?.get('boot')).toBe(PREV);
    expect(reqs.at(-1)!.get('unit')).toBe('rfd');
    await expect(page.locator('.ol-line').first()).toContainText('Earlier boot line');
    await expect(page.getByRole('checkbox', {name: 'Follow'})).toBeDisabled();
    await expect(button).not.toContainText('all boots');

    // the boot stays when the source changes
    await page.getByLabel('Source').selectOption('kernel');
    await expect(page).toHaveURL(new RegExp(`/system/log\\?source=kernel&boot=${PREV}$`));
    await expect.poll(() => reqs.at(-1)?.get('boot')).toBe(PREV);
    await expect(page.locator('.ol-line').first()).toContainText('(earlier boot)');
    // there is no "every boot" for the kernel's lines, and this boot brings Follow back
    await button.click();
    await expect(menu.getByRole('menuitemradio')).toHaveCount(3);
    await menu.getByRole('menuitemradio', {name: /This boot/}).click();
    await expect(page).toHaveURL(/\/system\/log\?source=kernel&boot=0$/);
    await expect(button).toContainText('Boot: this boot');
    await expect(page.locator('.ol-line').first()).not.toContainText('(earlier boot)');
    await expect(page.getByRole('checkbox', {name: 'Follow'})).toBeEnabled();
    // Escape closes the menu and gives the focus back
    await button.click();
    await expect(menu).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(menu).toBeHidden();
    await expect(button).toBeFocused();
});

test('in RAM the journal holds this boot alone: the menu says so and opens the storage settings', async ({page, context, baseURL}) => {
    await context.addCookies([{name: 'stub-journal-ram', value: '1', url: baseURL!}]);
    await page.goto('/system/log');
    await expect(page.locator('.ol-line').first()).toBeVisible();
    await bootButton(page).click();
    const menu = page.getByRole('menu', {name: 'Boot'});
    await expect(menu.getByRole('menuitemradio')).toHaveCount(2);
    await expect(menu).toContainText('Earlier boots need a persistent journal.');
    await menu.getByRole('menuitem', {name: 'Storage settings'}).click();
    await expect(menu).toBeHidden();
    const sheet = page.getByRole('dialog', {name: 'Log settings'});
    await expect(sheet).toBeVisible();
    await expect(sheet.getByRole('tab', {name: 'Journal'})).toHaveAttribute('aria-selected', 'true');
    await expect(sheet.locator('#ol-jr-storage')).toBeVisible();
});

test('the settings sheet: the levels, the journal and the history as tabs, Escape, and a change is asked about', async ({page}) => {
    page.on('dialog', (dlg) => {
        throw new Error(`native dialog: ${dlg.message()}`);
    });
    await page.goto('/system/log');
    await expect(page.locator('.ol-line').first()).toBeVisible();
    // the settings are no longer above the viewer
    await expect(page.getByRole('button', {name: 'Log levels'})).toHaveCount(0);
    const gear = page.getByRole('button', {name: 'Log settings'});
    await gear.click();
    const sheet = page.getByRole('dialog', {name: 'Log settings'});
    const tabs = sheet.getByRole('tab');
    // task 214: the history's storage is the third tab
    await expect(tabs).toHaveText(['Log levels', 'Journal', 'History']);
    await expect(tabs.nth(0)).toHaveAttribute('aria-selected', 'true');
    await expect(sheet.locator('#ol-lv-rfd')).toBeVisible();
    await expect(sheet.getByRole('tabpanel')).toHaveCount(1);
    // the arrow keys move between the tabs
    await tabs.nth(0).focus();
    await page.keyboard.press('ArrowRight');
    await expect(tabs.nth(1)).toHaveAttribute('aria-selected', 'true');
    await expect(tabs.nth(1)).toBeFocused();
    await expect(sheet.locator('#ol-jr-storage')).toBeVisible();
    await expect(sheet.locator('#ol-lv-rfd')).toHaveCount(0);
    await page.keyboard.press('ArrowRight');
    await expect(tabs.nth(2)).toBeFocused();
    await expect(sheet.locator('#ol-ds-mode')).toBeVisible();
    await page.keyboard.press('ArrowRight');
    await expect(tabs.nth(0)).toBeFocused();
    await page.keyboard.press('Escape');
    await expect(sheet).toBeHidden();
    await expect(gear).toBeFocused();

    // a change not saved yet survives a switch of tab, and is asked about before the sheet closes
    await gear.click();
    await expect(tabs.nth(0)).toHaveAttribute('aria-selected', 'true');
    await sheet.locator('#ol-lv-loghost').fill('syslog.example');
    await tabs.nth(1).click();
    await tabs.nth(0).click();
    await expect(sheet.locator('#ol-lv-loghost')).toHaveValue('syslog.example');
    await page.keyboard.press('Escape');
    const ask = page.getByRole('dialog', {name: 'Discard changes?'});
    await expect(ask).toBeVisible();
    await ask.getByRole('button', {name: 'Cancel'}).click();
    await expect(sheet.locator('#ol-lv-loghost')).toHaveValue('syslog.example');
    await page.keyboard.press('Escape');
    await ask.getByRole('button', {name: 'Discard'}).click();
    await expect(sheet).toBeHidden();
    // a closed sheet forgets it
    await gear.click();
    await expect(sheet.locator('#ol-lv-loghost')).toHaveValue('');
});

test('German, the dark theme, and nothing wider than a phone', async ({page}, info) => {
    await page.addInitScript(() => localStorage.setItem('ol.language', 'de'));
    await page.goto('/system/log?source=kernel');
    await expect(page.locator('.ol-line').first()).toBeVisible();
    await expect(page.getByLabel('Herkunft')).toContainText('Herkunft: Kernel');
    await expect(page.getByRole('button', {name: 'Nur Warnungen und Fehler'})).toBeVisible();
    await expect(page.getByRole('button', {name: /^Systemstart: dieser Systemstart/})).toBeVisible();
    await expect(page.getByLabel('Zeitstempel')).toContainText('seit Systemstart');
    const chip = page.getByRole('button', {name: 'Nur Warnungen und Fehler'});
    await chip.click();
    // the pressed chip in the accent colour, readable in either theme
    const [bg, fg] = await chip.evaluate((e) => [getComputedStyle(e).backgroundColor, getComputedStyle(e).color]);
    expect(bg).not.toBe(fg);
    if (info.project.name.includes('dark')) expect(await page.evaluate(() => matchMedia('(prefers-color-scheme: dark)').matches)).toBe(true);
    await page.getByRole('button', {name: 'Protokoll-Einstellungen'}).click();
    await expect(page.getByRole('dialog', {name: 'Protokoll-Einstellungen'}).getByRole('tab')).toHaveText(['Log-Level', 'Journal', 'Verlauf']);
    await page.keyboard.press('Escape');
    // nothing sticks out of the window
    const vw = page.viewportSize()!.width;
    expect(await pageWidth(page)).toBeLessThanOrEqual(vw);
});
