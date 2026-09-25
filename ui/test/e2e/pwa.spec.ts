import {expect, test} from '@playwright/test';

// task 193: the App is installable - the manifest with its own start URL and icons, the service
// worker emitted with the build's version, the page's head pointing at both
test('the manifest: standalone, /app/ as the start, the icons', async ({request}) => {
    const r = await request.get('/app.webmanifest');
    expect(r.ok()).toBe(true);
    const m = (await r.json()) as {display: string; start_url: string; icons: {src: string; purpose?: string}[]; theme_color: string};
    expect(m.display).toBe('standalone');
    expect(m.start_url).toBe('/app/');
    expect(m.icons.map((i) => i.src)).toEqual(['/icons/icon-192.png', '/icons/icon-512.png', '/icons/maskable-512.png']);
    expect(m.icons.find((i) => i.purpose === 'maskable')).toBeTruthy();
    for (const i of m.icons) expect((await request.get(i.src)).headers()['content-type']).toBe('image/png');
    expect((await request.get('/icons/apple-touch-icon.png')).ok()).toBe(true);
});

test('the service worker carries the build version and leaves the API alone', async ({request}) => {
    const r = await request.get('/sw.js');
    expect(r.ok()).toBe(true);
    const js = await r.text();
    expect(js).toMatch(/const VERSION = '[0-9a-f]{12}';/);
    expect(js).not.toContain('__VERSION__');
    expect(js).toContain("url.pathname.startsWith('/api/')");
});

test('the page links the manifest and the touch icon, with the theme colours of both schemes', async ({page}) => {
    await page.goto('/app');
    await expect(page.locator('link[rel="manifest"]')).toHaveAttribute('href', '/app.webmanifest');
    await expect(page.locator('link[rel="apple-touch-icon"]')).toHaveAttribute('href', '/icons/apple-touch-icon.png');
    await expect(page.locator('meta[name="theme-color"]')).toHaveCount(2);
    await expect(page.locator('meta[name="viewport"]')).toHaveAttribute('content', /viewport-fit=cover/);
});
