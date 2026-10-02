import {expect, test} from './fixtures';
import {fitsWindow} from './scroll';

// openccu-lite task 256 (the maintainer: "remove version panel from settings page and version
// display from top bar. add current version display to update page in the system update panel"):
// the installed version is named on the Updates page, above the release check; the top bar and
// Settings no longer carry it, and the API still does.

test('the Updates page names the installed version and the occulited build, beside the available release', async ({page}) => {
    await page.route('**/api/system/v1/system-update', async (route) => {
        if (route.request().method() !== 'GET') return route.fallback();
        const response = await route.fetch();
        const j = await response.json();
        j.feed.available = {version: '1.0.0-alpha.1', name: 'openccu-lite-ova-1.0.0-alpha.1.zip', size: 300 << 20, newer: true};
        await route.fulfill({response, json: j});
    });
    await page.goto('/system/updates');
    const installed = page.locator('[data-installed]');
    await expect(installed).toHaveText('Installed: openccu-lite 1.0.0-alpha.0 · OpenCCU 3.89.8.20260719 · occulited stub');
    await expect(installed.locator('[data-installed-build]')).toHaveClass(/ol-muted/);
    // right above the check's line, which names the available release
    const avail = page.getByText('Release 1.0.0-alpha.1 is available.');
    await expect(avail).toBeVisible();
    const a = (await installed.boundingBox())!;
    const b = (await avail.boundingBox())!;
    expect(b.y).toBeGreaterThan(a.y);
    expect(b.y - (a.y + a.height)).toBeLessThan(40);
    expect(await fitsWindow(page)).toBe(true);
});

test('a full commit hash as the build is shortened; German', async ({page}) => {
    await page.addInitScript(() => localStorage.setItem('ol.language', 'de'));
    await page.route('**/api/system/v1/health', async (route) => {
        const response = await route.fetch();
        await route.fulfill({response, json: {...(await response.json()), version: '6ce49acf22bcbfd80222cb79c6d1cd1fd1607f48'}});
    });
    await page.goto('/system/updates');
    await expect(page.locator('[data-installed]')).toHaveText('Installiert: openccu-lite 1.0.0-alpha.0 · OpenCCU 3.89.8.20260719 · occulited 6ce49ac');
});

test('no version in the top bar at any width, none on Settings; the API keeps it', async ({page, request}) => {
    for (const width of [1920, 1280, 700, 360]) {
        await page.setViewportSize({width, height: 800});
        await page.goto('/');
        await expect(page.locator('.ol-header .hmm-mono')).toHaveCount(0);
        await expect(page.locator('.ol-header')).not.toContainText('1.0.0-alpha.0');
    }
    await page.goto('/settings');
    await expect(page.getByRole('heading', {level: 1, name: 'Settings'})).toBeVisible();
    await expect(page.locator('main')).not.toContainText('1.0.0-alpha.0');
    await expect(page.getByText('About this system')).toHaveCount(0);
    const h = await (await request.get('/api/system/v1/health')).json();
    expect(h.release).toBe('1.0.0-alpha.0');
    const u = await (await request.get('/api/system/v1/system-update')).json();
    expect(u.running.lite).toBe('1.0.0-alpha.0');
});
