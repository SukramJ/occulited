import {expect, test} from '@playwright/test';

// The Services page: a typed filter searches every service, hidden categories included (28.3);
// the actions line up in one grid per row; a one-shot addon reads Completed (30.1).
test('the filter searches hidden services too', async ({page}) => {
    await page.goto('/system/services');
    await expect(page.getByRole('heading', {level: 1, name: 'Services'})).toBeVisible();
    const services = page.locator('table.ol-table').first();
    const rowsBefore = await services.locator('tbody tr').count();
    expect(rowsBefore).toBeGreaterThan(0);
    await page.getByPlaceholder('Filter').fill('occulited');
    await expect(services.locator('tbody tr')).toHaveCount(1);
    await expect(page.locator('.ol-toolbar')).toContainText('1 of');
});

// Task 80: the page shows no protocol, so the filter does not search it - no row is found by a text
// the page does not show. rfd answers with a protocol no other field of any service has.
test('the filter does not search the protocol', async ({page}) => {
    await page.route('**/api/system/v1/services', async (route) => {
        if (route.request().method() !== 'GET') return route.fallback();
        const response = await route.fetch();
        const body = (await response.json()) as {services: Record<string, unknown>[]};
        Object.assign(body.services.find((s) => s.id === 'rfd')!, {protocol: 'XML-RPC', port: 32001});
        await route.fulfill({response, json: body});
    });
    await page.goto('/system/services');
    const services = page.locator('table.ol-table').first();
    await expect(services.locator('tbody tr').first()).toBeVisible();
    await page.getByPlaceholder('Filter').fill('XML-RPC');
    await expect(page.getByText('Nothing matches.')).toBeVisible();
    await expect(page.locator('.ol-toolbar')).toContainText('0 of');
    // the same filter still finds rfd by what the page shows
    await page.getByPlaceholder('Filter').fill('BidCos-RF interface');
    await expect(services.locator('tbody tr')).toHaveCount(1);
});

// B-83: the page polls at the interval the box suggests, 5 s at the least, and not while the tab is
// hidden. The page's clock is Playwright's, so the test does not wait out the intervals.
//
// The page sets its next timer only when a poll has finished - after /services *and* /timers have
// answered - and that moment is the page's, not the test's: a test that counted the requests at the
// route and then moved the clock at once sometimes moved it before the timer existed, and the poll
// it waited for came too late (1 in 30 runs). So the page itself records, on its fake clock, when
// each GET /services is sent and when each poll has finished (the /timers body read; from there to
// the timer only microtasks run, so the timer is set whenever the test reads the record), the clock
// is only moved while no poll is running, and the gaps are checked on the page's clock.
type Polls = {sent: number[]; finished: number[]};

test('the poll waits as long as the box asks, and not in a hidden tab', async ({page}) => {
    await page.addInitScript(() => {
        const polls: Polls = {sent: [], finished: []};
        (window as unknown as {__polls: Polls}).__polls = polls;
        const original = window.fetch;
        window.fetch = function (input: RequestInfo | URL, init?: RequestInit) {
            const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
            const get = (init?.method ?? 'GET').toUpperCase() === 'GET';
            const answer = original.call(this, input, init);
            if (get && /\/api\/system\/v1\/services$/.test(url)) polls.sent.push(Date.now());
            if (!get || !/\/api\/system\/v1\/timers$/.test(url)) return answer;
            return answer.then((res) => {
                const text = res.text.bind(res);
                res.text = () => text().then((body) => {
                    polls.finished.push(Date.now());
                    return body;
                });
                return res;
            });
        } as typeof fetch;
    });
    await page.clock.install();
    let suggest = 12;
    await page.route('**/api/system/v1/services', async (route) => {
        if (route.request().method() !== 'GET') return route.fallback();
        const response = await route.fetch();
        const body = (await response.json()) as Record<string, unknown>;
        await route.fulfill({response, json: {...body, poll_seconds: suggest}});
    });
    const polls = () => page.evaluate(() => (window as unknown as {__polls: Polls}).__polls);
    const finished = async (n: number) => expect.poll(async () => (await polls()).finished.length).toBe(n);
    // moves the clock in steps until the n-th poll was sent, then waits until it has finished
    const nextPoll = async (n: number) => {
        for (let i = 0; i < 80 && (await polls()).sent.length < n; i++) await page.clock.runFor(500);
        await finished(n);
        expect((await polls()).sent.length).toBe(n);
    };

    await page.goto('/system/services');
    await finished(1);
    // 12 s asked: the second poll is sent 12 s after the first finished, not earlier
    suggest = 1; // what the second poll answers, set while no poll runs
    await nextPoll(2);
    let p = await polls();
    expect(p.sent[1]! - p.finished[0]!).toBeGreaterThanOrEqual(12_000);
    expect(p.sent[1]! - p.finished[0]!).toBeLessThan(12_500);
    // a box that asks for less than 5 s gets 5 s
    await nextPoll(3);
    p = await polls();
    expect(p.sent[2]! - p.finished[1]!).toBeGreaterThanOrEqual(5_000);
    expect(p.sent[2]! - p.finished[1]!).toBeLessThan(5_500);
    // hidden: no poll at all, however long; the handler clears the timer at once
    await page.evaluate(() => {
        Object.defineProperty(document, 'hidden', {configurable: true, get: () => true});
        document.dispatchEvent(new Event('visibilitychange'));
    });
    await page.clock.runFor(60_000);
    expect((await polls()).sent.length).toBe(3);
    // visible again: one poll at once (sent inside the handler), and the chain goes on from there
    await page.evaluate(() => {
        Object.defineProperty(document, 'hidden', {configurable: true, get: () => false});
        document.dispatchEvent(new Event('visibilitychange'));
    });
    p = await polls();
    expect(p.sent.length).toBe(4);
    expect(p.sent[3]! - p.finished[2]!).toBeGreaterThanOrEqual(60_000);
    await finished(4);
    await nextPoll(5);
    p = await polls();
    expect(p.sent[4]! - p.finished[3]!).toBeGreaterThanOrEqual(5_000);
    expect(p.sent[4]! - p.finished[3]!).toBeLessThan(5_500);
});

test('every row has the same action columns', async ({page}) => {
    await page.goto('/system/services');
    // the services table's rows; the timers table below shares the grid and the widths
    // (task 87, services-actions.spec.ts)
    const grids = page.locator('table.ol-table').first().locator('.ol-rowactions');
    const n = await grids.count();
    expect(n).toBeGreaterThan(3);
    const widths = new Set<number>();
    for (let i = 0; i < n; i++) {
        const box = await grids.nth(i).locator('.hmm-button').first().boundingBox();
        widths.add(Math.round(box?.width ?? 0));
    }
    expect([...widths].length, `button widths differ: ${[...widths].join(', ')}`).toBe(1);
});
