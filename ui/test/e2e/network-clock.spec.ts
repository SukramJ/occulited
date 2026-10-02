import {expect, test, type Page} from '@playwright/test';

// openccu-lite task 225 (the maintainer, 2026-09-24): the Time panel's clocks tick live with
// seconds - the system time from its last reading plus the time since, re-read every minute (an
// NTP step shows), the browser's time straight from its clock.
//
// B-36: the page's clock is paused at a fixed time before the page loads. Installed alone it flows
// with wall time, so on a busy runner a second passed during the load or between reading a
// second and stepping the clock, and the expected second was one off. The ticker steps at the
// browser's full seconds (20:19:31.000, …), so the system's answers carry half a second: each step
// then lands mid-second on the system's clock, never on a boundary.

const T = new Date('2026-09-24T20:19:30.200Z');

async function pausedClock(page: Page) {
    await page.clock.install({time: new Date(T.getTime() - 1000)});
    await page.clock.pauseAt(T);
}

test.use({timezoneId: 'UTC', locale: 'en-US'});

async function answerTime(page: Page, times: string[]) {
    let n = 0;
    await page.route('**/api/system/v1/time', async (route) => {
        if (route.request().method() !== 'GET') return route.fallback();
        const now = times[Math.min(n++, times.length - 1)];
        await route.fulfill({json: {tz: 'UTC', zone: 'UTC', ntp_servers: [], has_ntp: true, now}});
    });
}

test('the system time counts seconds from its reading and jumps with a re-read', async ({page}) => {
    await pausedClock(page);
    await answerTime(page, ['2026-09-24T18:00:00.500Z', '2026-09-24T19:00:00.500Z']);
    await page.goto('/system/network');
    const sys = page.locator('[data-panel="time"] [data-system-time]');
    await expect(sys).toHaveText(/6:00:00 PM/);
    await page.clock.runFor(1000);
    await expect(sys).toHaveText(/6:00:01 PM/);
    await page.clock.runFor(2000);
    await expect(sys).toHaveText(/6:00:03 PM/);
    // a minute later the page reads the time again: the system stepped an hour (NTP)
    await page.clock.runFor(60_000);
    await expect(sys).toHaveText(/7:00:0\d PM/);
});

test('the browser time ticks too', async ({page}) => {
    await pausedClock(page);
    await page.goto('/system/network');
    const hint = page.locator('[data-panel="time"]').getByText(/^This browser:/);
    await expect(hint).toContainText('8:19:30 PM');
    await page.clock.runFor(1000);
    await expect(hint).toContainText('8:19:31 PM');
    await page.clock.runFor(5000);
    await expect(hint).toContainText('8:19:36 PM');
});

test('in German: the clocks in the German format', async ({page}) => {
    await page.addInitScript(() => localStorage.setItem('ol.language', 'de'));
    await pausedClock(page);
    await answerTime(page, ['2026-09-24T18:00:00.000Z']);
    await page.goto('/system/network');
    await expect(page.locator('[data-panel="time"] [data-system-time]')).toHaveText(/24\.9\.2026, 18:00:00/);
    await expect(page.locator('[data-panel="time"]').getByText(/^Dieser Browser:/)).toContainText('20:19:30');
});
