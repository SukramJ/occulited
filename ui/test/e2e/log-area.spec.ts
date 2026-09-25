import {test, expect} from '@playwright/test';

// task 186: occulited's lines carry their area (OCCULITED_AREA); the Area filter asks for one and
// shows that area's lines alone, and "all" shows every line again
test.beforeEach(async ({page}) => {
    await page.addInitScript(() => localStorage.setItem('ol.log.filters', 'open'));
});

test('the Area filter shows the lines of one of occulited\'s areas', async ({page}) => {
    const reqs: string[] = [];
    page.on('request', (r) => {
        if (r.url().includes('/api/system/v1/log?')) reqs.push(new URL(r.url()).search);
    });
    await page.goto('/system/log');
    await expect(page.locator('.ol-line').first()).toBeVisible();
    const all = await page.locator('.ol-line').count();
    const area = page.locator('select[data-filter="area"]');
    await expect(area.locator('option')).toHaveText(['Area: all', 'Certificate (ACME)', 'Radio firmware', 'Addons', 'Metadata', 'HTTP requests', 'Logins', 'Status LED', 'lite-rpc']);
    await area.selectOption('addons');
    await expect.poll(() => reqs.at(-1)).toContain('area=addons');
    await expect.poll(() => page.locator('.ol-line').count()).toBeLessThan(all);
    await expect.poll(async () => (await page.locator('.ol-line .ol-src').allTextContents()).every((s) => s.startsWith('occulited'))).toBe(true);
    await area.selectOption('');
    await expect.poll(() => reqs.at(-1)).not.toContain('area=');
    await expect.poll(() => page.locator('.ol-line').count()).toBe(all);
});
