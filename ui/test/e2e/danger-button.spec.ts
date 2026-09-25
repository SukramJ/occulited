import {expect, test, type Locator, type Page} from '@playwright/test';

// B-110: a dangerous action's confirm button was red and turned blue on hover. ConfirmDialog painted
// `.primary.danger` red locally, and `.hmm-button.primary:hover` in app.css won on hover with the primary
// blue. The maintainer: "hover effect should be red instead and normal look should be standard". Every
// `danger` button now looks like a standard button when idle and turns red - the error colour as background
// and border, the page's ground as text - on hover and on keyboard focus; one rule in app.css. Light and
// dark are the two desktop projects; the phone hovers nothing and checks the idle look.

function token(page: Page, name: string) {
    return page.evaluate((v) => {
        const d = document.createElement('div');
        d.style.color = `var(${v})`;
        document.body.append(d);
        const c = getComputedStyle(d).color;
        d.remove();
        return c;
    }, name);
}

function paint(button: Locator) {
    return button.evaluate((el) => {
        const s = getComputedStyle(el);
        return {background: s.backgroundColor, border: s.borderTopColor, color: s.color, filter: s.filter};
    });
}

async function expectIdle(page: Page, button: Locator, what: string) {
    expect(await paint(button), `${what} idle: a standard button`).toMatchObject({
        background: await token(page, '--hmm-control-bg'),
        border: await token(page, '--hmm-border'),
        color: await token(page, '--hmm-fg'),
        filter: 'none',
    });
}

async function expectRed(page: Page, button: Locator, what: string) {
    expect(await paint(button), `${what}: red`).toMatchObject({
        background: await token(page, '--hmm-error'),
        border: await token(page, '--hmm-error'),
        color: await token(page, '--hmm-bg'),
        filter: 'none',
    });
}

/** the confirm button idle, hovered, idle again, and focused from the keyboard */
async function checkDanger(page: Page, dialog: Locator, confirm: string, isMobile: boolean) {
    const button = dialog.getByRole('button', {name: confirm, exact: true});
    await expect(button).toBeVisible();
    await expect(button).toHaveClass(/\bdanger\b/);
    await page.mouse.move(0, 0);
    await expectIdle(page, button, confirm);
    if (isMobile) return;
    await button.hover();
    await expectRed(page, button, `${confirm} hovered`);
    await page.mouse.move(0, 0);
    await expectIdle(page, button, `${confirm} after the hover`);
    // the keyboard: a question may open with the focus on this very button, put there after the mouse
    // click that opened it - which is no keyboard focus. So the focus leaves with Tab and comes back with
    // Shift+Tab, key by key, until it is on the button
    await page.keyboard.press('Tab');
    for (let i = 0; i < 8 && !(await button.evaluate((el) => el === document.activeElement)); i++) await page.keyboard.press('Shift+Tab');
    await expect(button).toBeFocused();
    await expectRed(page, button, `${confirm} focused from the keyboard`);
}

test('the firmware flash and a firmware delete: standard when idle, red on hover and on keyboard focus', async ({page, isMobile}) => {
    const writes: string[] = [];
    page.on('request', (r) => {
        if (r.method() !== 'GET' && r.url().includes('/radio/firmware')) writes.push(`${r.method()} ${r.url()}`);
    });
    await page.goto('/system/updates');
    const rows = page.locator('.fw-module').first().locator('tbody tr');
    await expect(rows).toHaveCount(3);
    const dialog = page.getByRole('dialog');

    await rows.nth(1).getByRole('button', {name: 'Flash'}).click();
    await expect(dialog).toContainText('Update the coprocessor');
    await checkDanger(page, dialog, 'Flash', isMobile);
    await page.keyboard.press('Escape');
    await expect(dialog).toBeHidden();

    await rows.nth(1).getByRole('button', {name: 'Delete'}).click();
    await expect(dialog).toBeVisible();
    await checkDanger(page, dialog, 'Delete', isMobile);
    await page.keyboard.press('Escape');
    await expect(dialog).toBeHidden();
    expect(writes, 'nothing was flashed or deleted').toEqual([]);
});

test('a question that is not dangerous keeps the primary look and its hover', async ({page, isMobile}) => {
    test.skip(isMobile, 'a phone hovers nothing');
    await page.addInitScript(() => {
        localStorage.setItem('ol.services.hideSystem', '1');
        localStorage.setItem('ol.services.hideOccu', '1');
    });
    await page.goto('/system/services');
    const rfd = page.locator('table.ol-table').first().locator('tbody tr').filter({has: page.locator('.sv-id', {hasText: /^rfd$/})});
    await expect(rfd).toHaveCount(1);
    await rfd.locator('.ol-rowactions button:has(.ol-acttext:text-is("Stop"))').click();
    const dialog = page.getByRole('dialog');
    await expect(dialog).toBeVisible();
    const confirm = dialog.locator('.hmm-button.primary');
    await expect(confirm).not.toHaveClass(/\bdanger\b/);
    await page.mouse.move(0, 0);
    expect((await paint(confirm)).background, 'idle: the accent').toBe(await token(page, '--hmm-accent'));
    await confirm.hover();
    const hovered = await paint(confirm);
    expect(hovered.background, 'hovered: still the accent').toBe(await token(page, '--hmm-accent'));
    expect(hovered.filter, 'hovered: the primary brightness').not.toBe('none');
    await page.keyboard.press('Escape');
    await expect(dialog).toBeHidden();
});
