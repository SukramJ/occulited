import {expect, test, type Page} from '@playwright/test';
import {PORT, pageScrolls, scrollbarWidth} from './scroll';

// B-93: opening a dialog shifted the whole page a few pixels to the right, and closing it shifted it
// back. lib/ModalDialog.svelte locks the page's scrolling while a modal is open by hiding the scrolling
// box's overflow; with a classic scrollbar that took the scrollbar away, the room grew by its width,
// and everything laid out against that width - the centred content, a table as wide as the page - moved.
// The lock pads the box by the width the scrollbar had (B-100 dropped the reserved `scrollbar-gutter`,
// which left a strip beside the top bar on every page). B-129: the box is `.ol-scrollport`, not the
// root, and the top bar is outside it - so the bar's buttons cannot move with the lock at all any more,
// and what the lock has to hold still is what is inside the port.
//
// Playwright's headless Chromium starts with --hide-scrollbars and draws no scrollbar at all, so the
// jump does not happen there: this file launches it without that switch. An engine that draws
// overlay scrollbars gives the scrollbar no width, so nothing can shift there, and the tests skip:
// the phone's emulation, and headless Firefox on Linux (tried 2026-09-12, also with GTK's overlay
// scrollbars switched off by pref). WebKit could not be launched on the WSL box it was written on.
test.use({launchOptions: {ignoreDefaultArgs: ['--hide-scrollbars']}});

type Where = {power: number; heading: number; table?: number};

// the x of the top bar's right-most button, the heading's centre (its box spans the page's width, so
// its centre is where centred content sits), and the right edge of a table as wide as the page
function where(page: Page): Promise<Where> {
    return page.evaluate(() => {
        const box = (sel: string) => {
            const el = document.querySelector(sel);
            if (!el) throw new Error(`no ${sel}`);
            return el.getBoundingClientRect();
        };
        const h1 = box('main h1');
        const table = document.querySelector('table.ol-table')?.getBoundingClientRect();
        return {power: box('.ol-powerbtn').x, heading: h1.x + h1.width / 2, ...(table ? {table: table.right} : {})};
    });
}

// the page is longer than the window and its scrollbar takes width; skipped where the engine draws
// overlay scrollbars, and a failure in desktop Chromium, where a lost switch would hide the bug
async function classicScrollbar(page: Page, isMobile: boolean, browserName: string) {
    const s = {long: await pageScrolls(page), bar: await scrollbarWidth(page)};
    expect(s.long, 'the page is longer than the window').toBe(true);
    test.skip(s.bar === 0 && (isMobile || browserName !== 'chromium'), `${isMobile ? 'the phone emulation' : browserName} draws overlay scrollbars here: they take no width, so nothing can shift`);
    expect(s.bar, 'a classic scrollbar takes width').toBeGreaterThan(0);
}

function expectSame(seen: [string, Where][]) {
    const [, first] = seen[0]!;
    for (const [what, w] of seen) expect(w, `${what}: ${JSON.stringify(w)}, before: ${JSON.stringify(first)}`).toEqual(first);
}

function rfdRow(page: Page) {
    return page.locator('table.ol-table').first().locator('tbody tr').filter({has: page.locator('.sv-id', {hasText: /^rfd$/})});
}

// every service listed and a short window: the page is well longer than the window
async function openServices(page: Page) {
    await page.addInitScript(() => {
        localStorage.setItem('ol.services.hideSystem', '0');
        localStorage.setItem('ol.services.hideOccu', '0');
    });
    await page.setViewportSize({width: 1440, height: 400});
    await page.goto('/system/services');
    await expect(rfdRow(page)).toHaveCount(1);
}

test.describe('a dialog does not move the page under it', () => {
    test('a confirmation dialog, closed with Cancel and with Escape, on the Services page', async ({page, isMobile, browserName}) => {
        await openServices(page);
        await classicScrollbar(page, isMobile, browserName);
        const seen: [string, Where][] = [['before', await where(page)]];
        const stop = rfdRow(page).locator('.ol-rowactions button:has(.ol-acttext:text-is("Stop"))');
        const dialog = page.getByRole('dialog');

        await stop.click({force: true});
        await expect(dialog).toBeVisible();
        seen.push(['the question open', await where(page)]);
        await dialog.getByRole('button', {name: 'Cancel'}).click({force: true});
        await expect(dialog).toBeHidden();
        seen.push(['after Cancel', await where(page)]);

        await stop.click({force: true});
        await expect(dialog).toBeVisible();
        seen.push(['the question open again', await where(page)]);
        await page.keyboard.press('Escape');
        await expect(dialog).toBeHidden();
        seen.push(['after Escape', await where(page)]);
        expectSame(seen);
    });

    test('a shell question over a page modal: the counted lock', async ({page, isMobile, browserName}) => {
        await openServices(page);
        await classicScrollbar(page, isMobile, browserName);
        const seen: [string, Where][] = [['before', await where(page)]];
        const editor = page.getByRole('dialog', {name: /Edit unit/});
        const question = page.getByRole('dialog', {name: 'Discard changes?'});
        const modalX = () => editor.evaluate((el) => el.getBoundingClientRect().x);
        const locked = () => page.evaluate((sel) => document.querySelector<HTMLElement>(sel)!.style.overflow, PORT);

        await rfdRow(page).getByRole('button', {name: 'More actions'}).click({force: true});
        await page.getByRole('menuitem', {name: 'Edit unit…'}).click();
        await expect(editor).toBeVisible();
        seen.push(['the editor open', await where(page)]);
        const editorX = await modalX();
        await editor.getByRole('textbox', {name: 'Override'}).fill('[Service]\nEnvironment=CHANGED=1\n');

        // Escape on a changed editor asks through the shell dialog, on top of it: a second holder
        await page.keyboard.press('Escape');
        await expect(question).toBeVisible();
        seen.push(['the question over the editor', await where(page)]);
        expect(await modalX(), 'the editor while the question is open').toBe(editorX);

        await question.getByRole('button', {name: 'Cancel'}).click();
        await expect(question).toBeHidden();
        await expect(editor).toBeVisible();
        seen.push(['the question gone, the editor open', await where(page)]);
        expect(await modalX(), 'the editor after the question').toBe(editorX);
        expect(await locked(), 'the editor still holds the lock').toBe('hidden');

        await page.keyboard.press('Escape');
        await question.getByRole('button', {name: 'Discard'}).click();
        await expect(page.getByRole('dialog')).toHaveCount(0);
        seen.push(['both closed', await where(page)]);
        expect(await locked(), 'the last holder gave the scrolling back').toBe('');
        expectSame(seen);
    });

    test('the power menu question on the Status page, whose content is centred', async ({page, isMobile, browserName}) => {
        // wider than the page's 1200 px, so the content is centred in the window
        await page.setViewportSize({width: 1440, height: 400});
        await page.goto('/');
        await expect(page.locator('main h1')).toBeVisible();
        await classicScrollbar(page, isMobile, browserName);
        const text = () => page.locator('main h1').evaluate((el) => {
            const r = document.createRange();
            r.selectNodeContents(el);
            return r.getBoundingClientRect().x;
        });
        const seen: [string, Where][] = [['before', await where(page)]];
        const textBefore = await text();
        const dialog = page.getByRole('dialog');

        await page.locator('.ol-powerbtn').click();
        await page.locator('.ol-powerpop [data-action="reboot"]').click();
        await expect(dialog).toBeVisible();
        seen.push(['the question open', await where(page)]);
        expect(await text(), 'the heading text while the question is open').toBe(textBefore);
        await page.keyboard.press('Escape');
        await expect(dialog).toBeHidden();
        seen.push(['after Escape', await where(page)]);
        expect(await text(), 'the heading text after Escape').toBe(textBefore);
        expectSame(seen);
    });

    // B-100: nothing is reserved any more - the lock pads the scrolling box by the width the scrollbar
    // had and takes it away with the last holder. B-129: that box is the port; the top bar no longer
    // needs to reach through anything, so there is no --ol-scrollbar-pad left to set.
    test('the lock pads the page by the scrollbar and gives it back', async ({page, isMobile, browserName}) => {
        await openServices(page);
        await classicScrollbar(page, isMobile, browserName);
        const bar = await scrollbarWidth(page);
        const port = () => page.evaluate((sel) => ({padding: document.querySelector<HTMLElement>(sel)!.style.paddingRight, pad: document.documentElement.style.getPropertyValue('--ol-scrollbar-pad')}), PORT);
        const seen: [string, Where][] = [['before', await where(page)]];
        const dialog = page.getByRole('dialog');

        await rfdRow(page).locator('.ol-rowactions button:has(.ol-acttext:text-is("Stop"))').click({force: true});
        await expect(dialog).toBeVisible();
        seen.push(['the question open', await where(page)]);
        expect(await port(), 'the port carries the scrollbar\'s width; the bar needs no reach-through').toEqual({padding: `${bar}px`, pad: ''});
        await page.keyboard.press('Escape');
        await expect(dialog).toBeHidden();
        seen.push(['after Escape', await where(page)]);
        expect(await port()).toEqual({padding: '', pad: ''});
        expectSame(seen);
    });
});
