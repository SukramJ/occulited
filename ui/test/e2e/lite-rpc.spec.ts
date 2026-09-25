import {expect, test, type Page} from '@playwright/test';

// task 77: lite-rpc - its panel on the Remote access page, and the open streams under it (task 223;
// under the registered clients from 2026-09-22). Each test keeps its own state (stub-ra, stub-lite).
async function own(page: Page, baseURL: string | undefined, lite = '') {
    const id = Math.random().toString(36).slice(2);
    await page.context().addCookies([
        {name: 'stub-ra', value: `ra-${id}`, url: baseURL!},
        {name: 'stub-lite', value: lite || `lite-${id}`, url: baseURL!},
    ]);
}

test('lite-rpc is always on: the panel says so, with the values', async ({page, baseURL}) => {
    await own(page, baseURL);
    await page.goto('/system/remote-access');
    const card = page.locator('[data-switch="lite"]');
    await expect(card.getByRole('checkbox')).toHaveCount(0);
    await expect(card).toContainText('always on');
    await expect(card.locator('[data-lite="streams"]')).toContainText('2 open · at most 2 per token or session, 16 in total');
    await expect(card.locator('[data-lite="buffer"]')).toContainText('300 s or 5000 events');
    await expect(card.locator('dt')).toHaveText(['Streams', 'Ring buffer', 'Heartbeat']);
});

test('the Remote access page lists the open streams and ends one', async ({page, baseURL}) => {
    await own(page, baseURL);
    await page.goto('/system/remote-access');
    await expect(page.getByRole('heading', {level: 3, name: 'Open streams'})).toBeVisible();
    const list = page.locator('[data-streams="2"]');
    await expect(list.locator('[data-stream="7"]')).toContainText('token home-assistant · SSE · 192.0.2.50');
    await expect(list.locator('[data-stream="7"]')).toContainText('1234 messages');
    await expect(list.locator('[data-stream="7"]')).toContainText('interface=HmIP-RF');
    await expect(list.locator('[data-stream="9"]')).toContainText('session of admin · WebSocket');
    await expect(list.locator('[data-stream="9"]')).toContainText('resync: gap');
    await list.locator('[data-stream="9"]').getByRole('button', {name: 'End stream'}).click();
    await expect(page.getByRole('dialog')).toContainText('End this stream?');
    await expect(page.getByRole('dialog')).toContainText('The connection of session of admin is closed.');
    const [req] = await Promise.all([
        page.waitForRequest((r) => r.method() === 'DELETE' && r.url().endsWith('/api/rpc/v1/streams/9')),
        page.getByRole('dialog').getByRole('button', {name: 'End stream'}).click(),
    ]);
    expect(req.method()).toBe('DELETE');
    await expect(page.locator('[data-streams="1"]')).toBeVisible();
    await expect(page.locator('[data-stream="9"]')).toHaveCount(0);
});

test('the Remote access page says when nothing is connected', async ({page, baseURL}) => {
    await own(page, baseURL, 'none');
    await page.goto('/system/remote-access');
    await expect(page.locator('[data-streams="none"]')).toHaveText('No stream open (2 per token or session, 16 in total).');
});
