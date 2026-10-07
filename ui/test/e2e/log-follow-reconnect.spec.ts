import {expect, test} from './fixtures';

// occulited task 19 (maintainer, 2026-10-06): Follow on the Log page stays on when its stream
// breaks - a restart of occulited behind lighttpd. The stream is opened again by itself, from the
// last line on screen (no line twice), and the log says "reconnected" where it was broken.

const sse = (lines: {cursor: string; message: string}[]) => ({
    status: 200,
    headers: {'Content-Type': 'text/event-stream'},
    body: ': stub\n\n' + lines.map((l) => `data: ${JSON.stringify({time: '2026-10-06T20:00:00Z', severity: 'info', tag: 'occulited', ...l})}\n\n`).join(''),
});

for (const lang of ['en', 'de'] as const) {
    test(`${lang}: Follow reconnects after the stream broke, resumes after the last line, and says so once`, async ({page}) => {
        if (lang === 'de') await page.addInitScript(() => localStorage.setItem('ol.language', 'de'));
        const asked: string[] = [];
        await page.route('**/api/system/v1/log/stream*', (route) => {
            const n = asked.push(new URL(route.request().url()).searchParams.get('after') ?? '');
            // 1: two lines, then the stream ends (occulited restarts); 2: lighttpd meanwhile; 3: occulited again
            if (n === 1) return route.fulfill(sse([{cursor: 'live-1', message: 'before the restart, one'}, {cursor: 'live-2', message: 'before the restart, two'}]));
            if (n === 2) return route.fulfill({status: 503, body: 'Service Unavailable'});
            if (n === 3) return route.fulfill(sse([{cursor: 'live-3', message: 'after the restart'}]));
            return route.fulfill({status: 200, headers: {'Content-Type': 'text/event-stream'}, body: ': idle\n\n'.repeat(1)});
        });
        await page.goto('/system/log');
        const box = page.locator('.lg-box');
        await expect(box.getByText('after the restart', {exact: true})).toBeVisible({timeout: 15_000});
        // each line once, Follow still on
        await expect(box.getByText('before the restart, two', {exact: true})).toHaveCount(1);
        await expect(box.getByText('before the restart, one', {exact: true})).toHaveCount(1);
        // (the phone's toolbar keeps the switch in its menu; the reconnect itself shows it stayed on)
        if (test.info().project.name !== 'phone') await expect(page.getByRole('checkbox', {name: lang === 'de' ? 'Live' : 'Follow'})).toBeChecked();
        // the 503 and the stream after it asked from the last line that had arrived
        expect(asked.slice(1, 3)).toEqual(['live-2', 'live-2']);
        // where the stream was opened again: one line saying so, after the line it broke after
        const mark = box.locator('[data-log-reconnected]');
        await expect(mark).toHaveCount(1);
        await expect(mark).toContainText(lang === 'de' ? 'neu verbunden' : 'reconnected');
        await expect(box.locator('.ol-line', {has: page.locator('[data-log-reconnected]')})).toContainText('before the restart, two');
    });
}
