import {expect, test} from '@playwright/test';

// The maintainer, 2026-09-20: a LAN gateway's access key is typed off a sticker (his
// HM-LGW-O-TW-W-EU carries "=u6%U!M8e3"), so the field has an eye that shows what was typed.
test('the access key is hidden until the eye is pressed', async ({page}) => {
    await page.goto('/system/lan-devices');
    await page.getByRole('button', {name: 'Add gateway'}).click();
    const field = page.getByLabel('the access key', {exact: true});
    await expect(field).toHaveAttribute('type', 'password');
    await field.fill('=u6%U!M8e3');
    const eye = page.locator('[data-secret-toggle]').first();
    await expect(eye).toHaveAttribute('aria-label', 'Show the access key');
    await expect(eye).toHaveAttribute('aria-pressed', 'false');
    await eye.click();
    await expect(field).toHaveAttribute('type', 'text');
    await expect(field).toHaveValue('=u6%U!M8e3');
    await expect(eye).toHaveAttribute('aria-label', 'Hide the access key');
    await expect(eye).toHaveAttribute('aria-pressed', 'true');
    await eye.click();
    await expect(field).toHaveAttribute('type', 'password');
});

test('in German the eye says what it shows', async ({page}) => {
    await page.addInitScript(() => localStorage.setItem('ol.language', 'de'));
    await page.goto('/system/lan-devices');
    await page.getByRole('button', {name: 'Gateway hinzufügen'}).click();
    const eye = page.locator('[data-secret-toggle]').first();
    await expect(eye).toHaveAttribute('aria-label', 'Den Zugangsschlüssel anzeigen');
    await eye.click();
    await expect(eye).toHaveAttribute('aria-label', 'Den Zugangsschlüssel verbergen');
});
