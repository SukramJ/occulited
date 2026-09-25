import {expect, test, type Page} from '@playwright/test';

// B-132: found on the Charly (2026-09-13) - opening Homematic Manager from the addon menu showed the
// whole openccu-lite shell inside the addon's frame, nested several levels deep (three top bars in the
// maintainer's screenshot). The addon's own session check had failed and it had sent the frame to the
// box's page; the shell then drew itself inside its own frame, and that page did it again.
//
// The stub's `stub-frame-loop` cookie makes every page under /addons/mh/ answer a redirect to `/`, so
// the frame gets exactly what the addon sent back there.

const NOTICE = /This addon sent you back to openccu-lite/;

/** every top bar in the tab, the frames' included */
async function topBars(page: Page): Promise<number> {
    let n = 0;
    for (const f of page.frames()) n += await f.locator('.ol-header').count();
    return n;
}

test('an addon whose frame comes back as the shell shows a notice, not a second shell', async ({page, baseURL}) => {
    const warnings: string[] = [];
    page.on('console', (m) => {
        if (m.type() === 'warning') warnings.push(m.text());
    });
    await page.context().addCookies([{name: 'stub-frame-loop', value: '1', url: baseURL!}]);
    await page.goto('/nav/mh');

    const frame = page.frameLocator('iframe[data-addon="mh"]');
    await expect(frame.getByText(NOTICE)).toBeVisible();
    await expect(frame.getByRole('button', {name: 'Open in new tab'})).toBeVisible();
    await expect(frame.getByRole('button', {name: 'Reload the page'})).toBeVisible();

    // exactly one top bar in the whole tab: the shell's own, outside the frame
    await expect.poll(() => topBars(page)).toBe(1);
    await expect(page.locator('.ol-header')).toHaveCount(1);

    // and the shell names the addon in the console, which is where the loop is diagnosed
    await expect.poll(() => warnings.join('\n')).toContain('"mh"');
});

test('the notice is in German too, and the shell itself is untouched', async ({page, baseURL}) => {
    await page.context().addCookies([{name: 'stub-frame-loop', value: '1', url: baseURL!}]);
    await page.addInitScript(() => localStorage.setItem('ol.language', 'de'));
    await page.goto('/nav/mh');
    await expect(page.frameLocator('iframe[data-addon="mh"]').getByText(/zurück zu openccu-lite geschickt/)).toBeVisible();
    await expect(page.frameLocator('iframe[data-addon="mh"]').getByRole('button', {name: 'Seite neu laden'})).toBeVisible();

    // away from the addon the shell is its ordinary self
    await page.locator('.ol-header a[href="/"]').first().click();
    await expect(page.getByRole('heading', {level: 1})).toBeVisible();
    await expect.poll(() => topBars(page)).toBe(1);
});

test('without the loop the addon page is framed as before', async ({page}) => {
    await page.goto('/nav/mh');
    const frame = page.frameLocator('iframe[data-addon="mh"]');
    await expect(frame.getByText('addon page stub: /addons/mh/')).toBeVisible();
    await expect(frame.getByText(NOTICE)).toHaveCount(0);
    await expect.poll(() => topBars(page)).toBe(1);
});
