import {expect, test} from '@playwright/test';

/*
 * Task 210 (the maintainer, 2026-09-23): "the menu items in the addon panels seem to have too much
 * spacing, should be a bit smaller (not the font, but the area that i see when hovering). also rework
 * the system menu: i dont like that it has so many different colors. make all the same color, a
 * decent/pastell accent fitting into our existing dark/bright color schemes."
 */

/** WCAG's contrast ratio of two rgb()/rgba() colours as getComputedStyle writes them */
function contrast(a: string, b: string): number {
    const lum = (c: string) => {
        const [r, g, bl] = (c.match(/[\d.]+/g) ?? []).slice(0, 3).map(Number).map((v) => {
            const x = v / 255;
            return x <= 0.03928 ? x / 12.92 : ((x + 0.055) / 1.055) ** 2.4;
        });
        return 0.2126 * r + 0.7152 * g + 0.0722 * bl;
    };
    const [hi, lo] = [lum(a), lum(b)].sort((x, y) => y - x);
    return (hi + 0.05) / (lo + 0.05);
}

test('the Addons menu rows are compact and still a usable target', async ({page}) => {
    await page.goto('/');
    await page.locator('.ol-addonsbtn').first().click();
    await expect(page.locator('nav.ol-sysmenu')).toHaveAttribute('data-phase', 'open');
    const items = await page.locator('.ol-addonpop .ol-menuitem').evaluateAll((els) =>
        els.map((e) => ({height: e.getBoundingClientRect().height, font: getComputedStyle(e).fontSize})),
    );
    expect(items.length).toBeGreaterThan(0);
    for (const i of items) {
        expect(i.height, JSON.stringify(i)).toBeGreaterThanOrEqual(24); // WCAG 2.5.8
        expect(i.height, JSON.stringify(i)).toBeLessThanOrEqual(37);
        expect(i.font).toBe('14px');
    }
});

test('the System menu has one pastel accent, readable in either scheme', async ({page}) => {
    await page.goto('/');
    await page.locator('.ol-systab').click();
    const panel = page.locator('nav.ol-sysmenu');
    await expect(panel).toHaveAttribute('data-phase', 'open');
    const tiles = await page.locator('.ol-systile').evaluateAll((els) => els.map((e) => ({bg: getComputedStyle(e).backgroundColor, ink: getComputedStyle(e).color})));
    expect(tiles.length).toBeGreaterThan(8);
    expect(new Set(tiles.map((t) => t.bg + ' ' + t.ink)).size, JSON.stringify(tiles)).toBe(1);
    const token = await page.evaluate(() => {
        const probe = document.createElement('span');
        probe.style.background = 'var(--ol-sys-tile-bg)';
        probe.style.color = 'var(--ol-sys-tile-ink)';
        document.body.append(probe);
        const out = {bg: getComputedStyle(probe).backgroundColor, ink: getComputedStyle(probe).color};
        probe.remove();
        return out;
    });
    expect(tiles[0]).toEqual(token);
    const panelBg = await panel.evaluate((p) => getComputedStyle(p).backgroundColor);
    expect(contrast(tiles[0].ink, tiles[0].bg)).toBeGreaterThanOrEqual(4.5);
    expect(contrast(tiles[0].ink, panelBg)).toBeGreaterThanOrEqual(4.5);
});
