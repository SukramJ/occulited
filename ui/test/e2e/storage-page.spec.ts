import {expect, test, type Page} from '@playwright/test';
import {fitsWindow} from './scroll';
import {buttonBecomesPanel} from './panels';

// openccu-lite task 228, phase 1: System → Storage - the USB sticks, what uses them, Format (an in-page panel since task 247)
// (exFAT by default, a required label, one danger click) and Safely remove.

async function own(page: Page, baseURL: string | undefined, variant?: string) {
    await page.context().addCookies([{name: 'stub-usb', value: variant ?? `u-${Math.random().toString(36).slice(2)}`, url: baseURL!}]);
    await page.goto('/system/storage');
}
const disk = (page: Page, name: string) => page.locator(`[data-disk="${name}"]`);

test('the sticks: partitions, what uses them, the menu entry before Backup', async ({page, baseURL}) => {
    await own(page, baseURL);
    await expect(page.getByRole('heading', {level: 2, name: 'USB sticks'})).toBeVisible();
    const a = disk(page, 'sda');
    await expect(a.locator('.ol-card-title')).toHaveText('LOGSTICK');
    await expect(a.locator('.ol-card-sub')).toContainText('SanDisk Ultra Fit');
    await expect(a.locator('[data-part="sda1"]')).toContainText('exFAT');
    await expect(a.locator('[data-part="sda1"]')).toContainText('mounted at /media/usb1');
    await expect(a.locator('[data-uses]')).toContainText("the journal's copies");
    await expect(a.locator('[data-uses]').getByRole('link', {name: 'the backup target USB stick'})).toHaveAttribute('href', '/system/backup#targets');
    await expect(disk(page, 'sdb').locator('[data-no-fs]')).toBeVisible();
    expect(await fitsWindow(page)).toBe(true);
});

test('Format: an in-page panel in the card, exFAT by default, the label checked, the erase asked (task 247)', async ({page, baseURL}) => {
    await own(page, baseURL);
    await disk(page, 'sda').getByRole('button', {name: 'Format', exact: true}).click();
    // task 247: the form opens in the stick's card, not in a dialog
    const dlg = disk(page, 'sda').locator('[data-format-panel]');
    await expect(disk(page, 'sda').getByRole('group', {name: 'Format the USB stick'})).toBeVisible();
    await expect(page.getByRole('dialog')).toHaveCount(0);
    await expect(dlg).toContainText('Everything on this stick is erased.');
    await expect(dlg).toContainText("It is in use by the journal's copies, the backup target USB stick: it is detached first");
    await expect(dlg.getByRole('radio', {name: /exFAT/})).toBeChecked();
    const label = dlg.locator('[data-format-label]');
    await expect(label).toHaveValue('LOGSTICK');
    await label.fill('');
    await expect(dlg.locator('[data-label-problem]')).toContainText('A label is required');
    await expect(page.locator('[data-action="format-confirm"]')).toBeDisabled();
    await label.fill('my stick');
    await expect(dlg.locator('[data-label-problem]')).toContainText('Letters, digits');
    await dlg.getByRole('radio', {name: /ext4/}).check();
    await label.fill('JOURNAL_2026');
    // the erase asks the shell's danger question first; Cancel there sends nothing
    let posts = 0;
    page.on('request', (r) => {
        if (r.method() === 'POST' && r.url().endsWith('/storage/usb/sda/format')) posts++;
    });
    await page.locator('[data-action="format-confirm"]').click();
    const ask = page.getByRole('dialog');
    await expect(ask).toContainText('Everything on this stick is erased and it is formatted as ext4 with the label JOURNAL_2026.');
    await ask.getByRole('button', {name: 'Cancel'}).click();
    await expect(ask).toHaveCount(0);
    expect(posts).toBe(0);
    await page.locator('[data-action="format-confirm"]').click();
    const [req] = await Promise.all([
        page.waitForRequest((r) => r.method() === 'POST' && r.url().endsWith('/storage/usb/sda/format')),
        page.getByRole('dialog').getByRole('button', {name: 'Erase and format'}).click(),
    ]);
    expect(req.postDataJSON()).toEqual({fs: 'ext4', label: 'JOURNAL_2026'});
    await expect(dlg).toHaveCount(0);
    await expect(page.locator('[data-notice="storage"]')).toContainText('formatted as ext4 with the label JOURNAL_2026');
    await expect(disk(page, 'sda').locator('.ol-card-title')).toHaveText('JOURNAL_2026');
});

test('a blank stick gets OPENCCU; Cancel changes nothing', async ({page, baseURL}) => {
    await own(page, baseURL);
    const button = disk(page, 'sdb').getByRole('button', {name: 'Format', exact: true});
    await button.click();
    await expect(page.locator('[data-format-label]')).toHaveValue('OPENCCU');
    await disk(page, 'sdb').getByRole('group', {name: 'Format the USB stick'}).getByRole('button', {name: 'Cancel'}).click();
    await expect(page.locator('[data-format-dialog]')).toHaveCount(0);
    // task 268: the button becomes the panel and comes back after Cancel
    await buttonBecomesPanel(disk(page, 'sdb').locator('[data-action="format"]'), disk(page, 'sdb').getByRole('group', {name: 'Format the USB stick'}));
    await expect(page.locator('[data-format-dialog]')).toHaveCount(0);
    await expect(disk(page, 'sdb').locator('[data-no-fs]')).toBeVisible();
});

test('Safely remove: the question, then "can be pulled out now"', async ({page, baseURL}) => {
    await own(page, baseURL);
    await disk(page, 'sda').getByRole('button', {name: 'Safely remove'}).click();
    const dialog = page.getByRole('dialog');
    await expect(dialog).toContainText('the journal makes a last copy and carries on in RAM');
    await dialog.getByRole('button', {name: 'Remove'}).click();
    await expect(page.locator('[data-notice="storage"]')).toContainText('LOGSTICK can be pulled out now.');
    await expect(disk(page, 'sda').locator('[data-part="sda1"]')).toContainText('not mounted');
});

test('no sticks; a user sees the list without buttons; German', async ({page, baseURL}) => {
    await own(page, baseURL, 'none');
    await expect(page.locator('[data-storage-empty]')).toHaveText('No USB stick is plugged in.');
    await page.route('**/api/auth/v1/state', (r) => r.fulfill({json: {setup_required: false, authenticated: true, user: 'monitor', role: 'user', must_change_password: false, sid: 'U1'}}));
    await own(page, baseURL);
    await expect(disk(page, 'sda')).toBeVisible();
    await expect(page.getByRole('button', {name: 'Format', exact: true})).toHaveCount(0);
    await page.unroute('**/api/auth/v1/state');
    await page.addInitScript(() => localStorage.setItem('ol.language', 'de'));
    await own(page, baseURL);
    await expect(page.getByRole('heading', {level: 2, name: 'USB-Sticks'})).toBeVisible();
    await expect(disk(page, 'sda').getByRole('button', {name: 'Formatieren', exact: true})).toBeVisible();
    await expect(disk(page, 'sda').getByRole('button', {name: 'Sicher entfernen'})).toBeVisible();
});
