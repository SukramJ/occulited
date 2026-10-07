import {expect, shellStreamInWindow, test} from './fixtures';

// occulited task 19: the shell's EventSources after a restart of occulited behind lighttpd - the
// stream ends, the browser's reconnect gets lighttpd's 503 while occulited is down, and an
// EventSource gives up for good on that. The shell opens such a stream again by itself.
// occulited B-53: the service messages are a topic of the shell's stream; its hub, run in the window
// here (Playwright cannot route a SharedWorker's requests), is the worker's.

const MESSAGES = '/api/system/v1/stream';

for (const [where, path] of [
    ['the Status page', '/'],
    ['the App', '/app/e/rooms/og/bad'],
] as const) {
    test(`${where}: the shell's stream comes back after a restart of occulited`, async ({page}) => {
        const answers: number[] = [];
        await shellStreamInWindow(page);
        await page.route(`**${MESSAGES}?*`, (route) => {
            const n = answers.length;
            // 1: the stream, which ends (the restart); 2: lighttpd while occulited is down; 3: occulited again
            if (n === 0) {
                answers.push(200);
                return route.fulfill({status: 200, headers: {'Content-Type': 'text/event-stream'}, body: 'retry: 200\n\n'});
            }
            if (n === 1) {
                answers.push(503);
                return route.fulfill({status: 503, body: 'Service Unavailable'});
            }
            answers.push(0);
            return route.continue();
        });
        await page.goto(path);
        await expect.poll(() => answers, {timeout: 15_000}).toEqual([200, 503, 0]);
    });
}
