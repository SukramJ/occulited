import {expect, test} from '@playwright/test';

// B-155: while a radio firmware flash runs, the page polls the attempt every 2 s. The run's log box
// must stay filled across those polls - it emptied for a frame at every one (a white "lightning"
// on the Interfaces page) - and be fetched once per 2 s, not twice.
test('the running flash log stays filled while the page polls', async ({page, request}) => {
    const base = await (await request.get('/api/system/v1/radio/firmware')).json();
    let polls = 0;
    await page.route('**/api/system/v1/radio/firmware', (r) => {
        polls++;
        // a new object each time, as the real attempt carries a fresh time
        return r.fulfill({json: {...base, running: {module: 'HmIP-RFUSB', device_node: '/dev/raw-uart', file: '/usr/local/etc/config/radio-firmware/HmIP-RFUSB/dualcopro_update_blhmip-4.4.22.eq3', file_version: '4.4.22', started: new Date().toISOString(), ok: false, exit: 0, run_id: '20260912T120000-5eed0002'}}});
    });
    let logReads = 0;
    page.on('request', (q) => { if (q.url().includes('/api/system/v1/log?run=')) logReads++; });
    await page.goto('/system/updates');
    const box = page.locator('pre.fw-log');
    await expect(box).not.toBeEmpty();
    await box.evaluate((el) => {
        (window as unknown as {emptied: number}).emptied = 0;
        new MutationObserver(() => { if (!el.textContent) (window as unknown as {emptied: number}).emptied++; }).observe(el, {childList: true, characterData: true, subtree: true});
    });
    const pollsBefore = polls;
    const readsBefore = logReads;
    await page.waitForTimeout(6500);
    expect(polls - pollsBefore).toBeGreaterThanOrEqual(3);
    expect(await page.evaluate(() => (window as unknown as {emptied: number}).emptied)).toBe(0);
    // the log's own 2 s timer: three or four reads in 6.5 s, not two per poll
    expect(logReads - readsBefore).toBeLessThanOrEqual(4);
});
