import {expect, test, type Page} from '@playwright/test';

// The addon whose frontend is open (/nav/<id>) is marked in the addon dropdown. app.css named three
// tokens for it that were never defined (--hmm-bg-active, --hmm-bg-muted, --hmm-bg-hover), so the
// row had no background in either theme; it now uses the one every other active menu entry has.
const menu = (page: Page) => page.locator('.ol-menu').first();
const row = (page: Page, name: string) => menu(page).getByRole('menu').locator('.ol-menurow').filter({has: page.locator('.ol-addonname', {hasText: new RegExp(`^${name}$`)})});

test("the open addon's row in the dropdown has a background of its own", async ({page}) => {
    await page.goto('/nav/red');
    await menu(page).locator('.ol-menubtn').click();
    const active = row(page, 'RedMatic');
    await expect(active).toHaveClass(/active/);
    const background = (name: string) => row(page, name).locator('.ol-menuitem').evaluate((el) => getComputedStyle(el).backgroundColor);
    const accentBg = await page.evaluate(() => {
        // the token resolved the way the browser resolves it, whatever the theme
        const probe = document.createElement('div');
        probe.style.background = 'var(--hmm-accent-bg)';
        document.body.append(probe);
        const c = getComputedStyle(probe).backgroundColor;
        probe.remove();
        return c;
    });
    expect(accentBg).not.toBe('rgba(0, 0, 0, 0)');
    await expect.poll(() => background('RedMatic')).toBe(accentBg);
    expect(await background('Homematic-Manager')).not.toBe(accentBg);
});
