import {expect, test, type Page} from '@playwright/test';

// openccu-lite task 243 (the maintainer): "lighttpd should appear under system-services even when system
// services are hidden, move it to the "core" category". The units the web interface runs through
// (lighttpd, occulited, its helper) offer Restart - the page reconnects - and neither Stop nor Disable.

const row = (page: Page, id: string) => page.locator(`tr[data-service="${id}"]`);

async function open(page: Page, language: 'en' | 'de' = 'en') {
    await page.addInitScript((l) => {
        localStorage.setItem('ol.language', l);
        localStorage.setItem('ol.services.hideSystem', '1');
        localStorage.setItem('ol.services.hideOccu', '1');
    }, language);
    await page.goto('/system/services');
    await expect(row(page, 'rfd')).toBeVisible();
}

test('lighttpd is a core service: shown with the system services hidden', async ({page}) => {
    await open(page);
    await expect(row(page, 'lighttpd')).toBeVisible();
    await expect(row(page, 'sshd')).toHaveCount(0);
});

test('lighttpd and occulited: Restart, no Stop, no Disable', async ({page, isMobile}) => {
    await open(page);
    for (const id of ['lighttpd', 'occulited']) {
        const r = row(page, id);
        await expect(r.getByRole('button', {name: 'Restart'})).toHaveCount(1);
        await expect(r.getByRole('button', {name: 'Stop'})).toHaveCount(0);
        await r.getByRole('button', {name: 'More actions'}).click();
        await expect(page.getByRole('menuitem', {name: 'Disable'})).toHaveCount(0);
        await expect(page.getByRole('menuitem', {name: 'Edit unit…'})).toBeVisible();
        await page.keyboard.press('Escape');
        if (isMobile) await page.mouse.click(5, 5);
    }
    // rfd keeps its Stop
    await expect(row(page, 'rfd').getByRole('button', {name: 'Stop'})).toHaveCount(1);
});

test('restarting lighttpd says the page goes away and reconnects', async ({page}) => {
    await open(page);
    const sent: string[] = [];
    await page.route('**/api/system/v1/services/lighttpd/restart', async (route) => {
        sent.push(route.request().method());
        await route.abort('connectionreset');
    });
    let down = 2;
    await page.route('**/api/system/v1/health', async (route) => {
        if (down-- > 0) return route.abort('connectionrefused');
        return route.continue();
    });
    await row(page, 'lighttpd').getByRole('button', {name: 'Restart'}).click();
    const dialog = page.getByRole('dialog');
    await expect(dialog).toContainText('The web interface runs through it: this page is gone for a few seconds and reconnects by itself.');
    await dialog.getByRole('button', {name: 'OK'}).click();
    await expect(page.locator('[data-reconnecting]')).toBeVisible();
    await expect(page.locator('[data-reconnecting]')).toHaveCount(0, {timeout: 15_000});
    expect(sent).toEqual(['POST']);
    // the aborted answer is no error on the page
    await expect(page.locator('pre.ol-log')).toHaveCount(0);
});

test('German', async ({page}) => {
    await open(page, 'de');
    await expect(row(page, 'lighttpd').getByRole('button', {name: 'Neu starten'})).toHaveCount(1);
    await expect(row(page, 'lighttpd').getByRole('button', {name: 'Stoppen'})).toHaveCount(0);
});
