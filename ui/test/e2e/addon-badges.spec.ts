import {expect, test, type Locator, type Page} from '@playwright/test';

// occulited task 10 (the maintainer, 2026-10-03): the Addons page's badges say by their colour what
// they mean - a positive statement (confined, starts early) in the kit's ok colour, a caution
// (untested, own updater, root) in its warn colour, a fault in its error colour; the rest neutral.
// Every colour is a token, so the pairs are checked in both themes (the desktop-light and
// desktop-dark projects), with the text readable on the badge (WCAG AA for small text, 4.5:1).

// the stub keeps the early-start switches per browser under this cookie (early-start.spec.ts): a
// test of its own, so another file switching Homematic-Manager's off does not reach this one
const own = async (page: Page, baseURL: string | undefined, info: {project: {name: string}; title: string}) => {
    await page.context().addCookies([{name: 'stub-early', value: `${info.project.name}-${info.title}`.replace(/[^\w-]/g, '_'), url: baseURL}]);
};
const row = (page: Page, name: string) => page.locator('.ad-card').filter({has: page.locator('.ad-name', {hasText: new RegExp(`^\\W*${name}$`)})}).first();

// the token's colour as the browser resolves it, through a probe painted with it
const token = (page: Page, name: string) => page.evaluate((n) => {
    const p = document.createElement('span');
    p.style.background = `var(${n})`;
    document.body.append(p);
    const c = getComputedStyle(p).backgroundColor;
    p.remove();
    return c;
}, name);

const rgb = (c: string) => (c.match(/\d+(\.\d+)?/g) ?? []).slice(0, 3).map(Number);
const lum = (c: string) => {
    const [r, g, b] = rgb(c).map((v) => {
        const s = v / 255;
        return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
    });
    return 0.2126 * r + 0.7152 * g + 0.0722 * b;
};
const contrast = (a: string, b: string) => {
    const [x, y] = [lum(a), lum(b)].sort((p, q) => q - p);
    return (x + 0.05) / (y + 0.05);
};

const painted = async (page: Page, badge: Locator, cls: string, tok: string) => {
    await expect(badge).toHaveClass(new RegExp(`\\b${cls}\\b`));
    const {bg, fg} = await badge.evaluate((el) => ({bg: getComputedStyle(el).backgroundColor, fg: getComputedStyle(el).color}));
    expect(bg).toBe(await token(page, tok));
    expect(contrast(bg, fg)).toBeGreaterThanOrEqual(4.5);
};

test('confined and starts early are green, untested and own updater orange, in both themes', async ({page, baseURL}, info) => {
    await own(page, baseURL, info);
    await page.goto('/addons');
    const mh = row(page, 'Homematic-Manager');
    await expect(mh.locator('.ad-confined')).toHaveText('confined');
    await painted(page, mh.locator('.ad-confined'), 'ok', '--hmm-ok');
    await painted(page, mh.locator('.ad-early'), 'ok', '--hmm-ok');
    await painted(page, row(page, 'TM Devices').locator('.ad-untested'), 'warn', '--hmm-warn');
    await painted(page, row(page, 'XML-API').locator('.ol-badge', {hasText: 'own updater'}), 'warn', '--hmm-warn');
    // a neutral one stays the sunken pill
    const pre = row(page, 'TM Devices').locator('.ol-badge', {hasText: 'prerelease'});
    expect(await pre.evaluate((el) => getComputedStyle(el).backgroundColor)).toBe(await token(page, '--hmm-bg-sunken'));
});
