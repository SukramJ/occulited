import {expect, test} from './fixtures';

// Task 102 (D-59): a firmware flash's and an ACME attempt's lines are in the journal. The pages read
// them through GET /log?run=<run_id>, a finished run the journal no longer holds says so, and the
// run's Show log link opens the Log page on that run. The stub answers the runs it knows
// (test/stub/server.mjs, STUB_RUNS) and run_missing for any other.

const CERT_RUN = '20260912T120000-5eed0001';
const FLASH_RUN = '20260912T120000-5eed0002';

test("a running ACME test shows its lines from the log API, and the link opens the run on the Log page", async ({page}) => {
    const runReads: string[] = [];
    page.on('request', (r) => {
        if (r.url().includes('/api/system/v1/log?') && r.url().includes('run=')) runReads.push(new URL(r.url()).searchParams.get('run') ?? '');
    });
    await page.goto('/system/certificates');
    await page.getByRole('radio', {name: 'ACME'}).check();
    await page.locator('#ol-cert-names').fill('ccu.example.org');
    await page.getByRole('button', {name: 'Test'}).click();
    await expect(page.getByRole('heading', {level: 2, name: /Running: Test/})).toBeVisible();
    const log = page.locator('pre.cert-log');
    await expect(log).toContainText('test: ccu.example.org via http-01');
    await expect(log).toContainText('Obtaining bundled SAN certificate');
    await expect(log).toHaveAttribute('data-run', CERT_RUN);
    expect(runReads).toContain(CERT_RUN);
    const show = page.getByRole('link', {name: 'Show log'});
    await expect(show).toHaveAttribute('href', `/system/log?run=${CERT_RUN}`);
    await show.click();
    await expect(page).toHaveURL(new RegExp(`/system/log\\?run=${CERT_RUN}$`));
    // task 104: below 700 px the filters are a card that starts closed, and its badge counts the run
    const toggle = page.locator('.lg-filters-toggle');
    if (await toggle.isVisible()) {
        await expect(toggle.locator('[data-filter-count]')).toHaveText('1');
        if ((await toggle.getAttribute('aria-expanded')) !== 'true') await toggle.click();
    }
    await expect(page.locator(`.lg-run[data-run="${CERT_RUN}"]`)).toBeVisible();
    await expect(page.locator('.ol-line').first()).toContainText('test: ccu.example.org via http-01');
    await expect(page.locator('.ol-line', {hasText: 'Sample log line'})).toHaveCount(0);
    // the ✕ takes the run away and the whole log comes back
    await page.getByRole('button', {name: 'Show the whole log'}).click();
    await expect(page).toHaveURL(/\/system\/log$/);
    await expect(page.locator('.lg-run')).toHaveCount(0);
});

test('a flash in flight shows its phases from the journal', async ({page}) => {
    await page.goto('/system/updates');
    const card = page.locator('.fw-module').first();
    await card.locator('tbody tr').nth(1).getByRole('button', {name: 'Flash'}).click();
    await page.getByRole('dialog').getByRole('button', {name: 'Flash'}).click();
    const log = page.locator('pre.fw-log');
    await expect(log).toContainText('hmipserver stopped');
    await expect(log).toContainText('hmip-copro-update: writing 104616 bytes');
    await expect(log).toHaveAttribute('data-run', FLASH_RUN);
});

for (const language of ['en', 'de'] as const) {
    test(`a finished flash the journal no longer holds says so (${language})`, async ({page}) => {
        await page.addInitScript((l) => localStorage.setItem('ol.language', l), language);
        // the last flash was before a reboot of a box whose journal is in RAM
        await page.route('**/api/system/v1/radio/firmware', async (route) => {
            if (route.request().method() !== 'GET') return route.fallback();
            const r = await route.fetch();
            const st = await r.json();
            st.last = {module: 'HMIP-RFUSB', device_node: '/dev/raw-uart', file: '/firmware/HmIP-RFUSB/dualcopro_update_blhmip-4.4.18.eq3', file_version: '4.4.18', started: '2026-09-01T01:00:00Z', finished: '2026-09-01T01:02:00Z', ok: true, before: '4.4.18', after: '4.4.18', exit: 0, run_id: '20260901T010000-0000dead'};
            await route.fulfill({json: st});
        });
        await page.goto('/system/updates');
        const gone = page.locator('[data-notice="run-gone"]');
        await expect(gone).toContainText(language === 'en' ? 'The log of this run is no longer in the journal' : 'Das Protokoll dieses Laufs ist nicht mehr im Journal');
        await expect(gone.getByRole('link')).toHaveAttribute('href', '/system/log?settings=journal');
        await expect(page.locator('pre.fw-log')).toHaveCount(0);
    });
}
