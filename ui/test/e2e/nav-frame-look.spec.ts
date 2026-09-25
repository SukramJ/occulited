import {expect, test} from '@playwright/test';

// B-200: the frame of an addon's menu entry under /nav/<id> gets theme= and lang= on its URL, as
// the embedding contract says and as the settings frame always did - an addon that reads only the
// URL starts in the right look. A change later is a message; the frame is not reloaded for it.

test('a nav.d page and a kept addon frontend carry theme= and lang=', async ({page, baseURL}) => {
    await page.context().addCookies([{name: 'stub-nav-page', value: '1', url: baseURL!}]);
    await page.addInitScript(() => {
        localStorage.setItem('ol.theme', 'dark');
        localStorage.setItem('ol.language', 'de');
    });
    await page.goto('/nav/manual');
    const nav = page.locator('iframe.ol-frame[data-addon="manual"]');
    await expect(nav).toHaveAttribute('src', '/stub-manual/?theme=dark&lang=de');
    await page.goto('/nav/red');
    await expect(page.locator('iframe.ol-kept-frame[src^="/addons/red/"]')).toHaveAttribute('src', /^\/addons\/red\/(\?sid=@\w+@&|\?)theme=dark&lang=de$/);
});

test('a theme change does not reload a kept frontend: its URL keeps the first look', async ({page}) => {
    await page.addInitScript(() => localStorage.setItem('ol.theme', 'light'));
    await page.goto('/nav/red');
    const red = page.locator('iframe.ol-kept-frame[src^="/addons/red/"]');
    await expect(red).toHaveAttribute('src', /theme=light/);
    await red.evaluate((f: HTMLIFrameElement) => ((f.contentWindow as unknown as {__mark: number}).__mark = 1));
    await page.locator('a.ol-iconlink[href="/settings"]').click();
    await page.locator('select').nth(1).selectOption('dark');
    await page.goBack();
    await expect(page).toHaveURL(/\/nav\/red$/);
    await expect(red).toBeVisible();
    await expect(red).toHaveAttribute('src', /theme=light/);
    expect(await red.evaluate((f: HTMLIFrameElement) => (f.contentWindow as unknown as {__mark?: number}).__mark)).toBe(1);
});
