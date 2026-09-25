import {expect, test, type Page} from '@playwright/test';
import {fitsWindow} from './scroll';

// openccu-lite task 228, phase 2: System → Storage's network shares - the list with each share's
// state, adding an SMB share (the name follows the server), editing with the password write-only,
// Test, Mount and Unmount, Remove, a container, a user without the buttons, German.

async function own(page: Page, baseURL: string | undefined, variant?: string) {
    await page.context().addCookies([{name: 'stub-shares', value: variant ?? `s-${Math.random().toString(36).slice(2)}`, url: baseURL!}]);
    await page.goto('/system/storage');
}
const share = (page: Page, id: string) => page.locator(`[data-share="${id}"]`);
// task 241: the form is an in-page panel (lib/Disclosure.svelte), not a dialog
const form = (page: Page) => page.locator('.ol-disclosure');

test('the list: a mounted SMB share with its space, an idle read-only NFS export', async ({page, baseURL}) => {
    await own(page, baseURL);
    await expect(page.getByRole('heading', {level: 2, name: 'Network shares'})).toBeVisible();
    const nas = share(page, 'nas');
    await expect(nas.locator('.ol-card-title')).toContainText('SMB share');
    await expect(nas.locator('.ol-card-sub')).toHaveText('//nas.lan/backup');
    await expect(nas.locator('[data-state]')).toHaveText('mounted');
    await expect(nas.locator('[data-space]')).toContainText('812.0 GB free of 2.0 TB');
    await expect(nas).toContainText('Mount point /media/net/nas');
    await expect(nas.getByRole('button', {name: 'Unmount'})).toBeVisible();
    const media = share(page, 'media');
    await expect(media.locator('.ol-card-title')).toContainText('read-only');
    await expect(media.locator('[data-state]')).toHaveText('mounted when used');
    await expect(media.getByRole('button', {name: 'Mount'})).toBeVisible();
    expect(await fitsWindow(page)).toBe(true);
});

test('add an SMB share: the name follows the server, the fields are sent as the API wants them', async ({page, baseURL}) => {
    await own(page, baseURL);
    await page.getByRole('button', {name: 'Add share'}).click();
    const dlg = form(page);
    await expect(dlg).toContainText('Add a network share');
    await expect(dlg.getByRole('radio', {name: /SMB/})).toBeChecked();
    await dlg.locator('[data-field="server"]').fill('truenas.lan');
    await expect(dlg.locator('[data-field="name"]')).toHaveValue('truenas');
    await expect(dlg).toContainText('mounted at /media/net/truenas');
    await dlg.locator('[data-field="path"]').fill('\\ccu\\');
    await dlg.locator('[data-field="user"]').fill('openccu');
    await dlg.locator('[data-field="password"]').fill('pa55');
    await dlg.locator('[data-field="version"]').selectOption('3.1.1');
    // a name of one's own: checked before it is sent
    await dlg.locator('[data-field="name"]').fill('True-NAS');
    await expect(dlg.locator('[data-name-problem]')).toContainText('Lower-case letters and digits');
    await expect(dlg.getByRole('button', {name: 'Save'})).toBeDisabled();
    await dlg.locator('[data-field="name"]').fill('tank');
    await dlg.locator('[data-field="server"]').fill('truenas.lan '); // the name stays the user's
    await expect(dlg.locator('[data-field="name"]')).toHaveValue('tank');
    const [req] = await Promise.all([
        page.waitForRequest((r) => r.method() === 'POST' && r.url().endsWith('/storage/shares')),
        dlg.getByRole('button', {name: 'Save'}).click(),
    ]);
    expect(req.postDataJSON()).toEqual({id: 'tank', kind: 'cifs', server: 'truenas.lan', path: 'ccu', version: '3.1.1', read_only: false, seal: false, user: 'openccu', password: 'pa55'});
    await expect(form(page)).toHaveCount(0);
    await expect(share(page, 'tank').locator('.ol-card-sub')).toHaveText('//truenas.lan/ccu');
});

test('add an NFS export read-only; a name that is taken', async ({page, baseURL}) => {
    await own(page, baseURL);
    await page.getByRole('button', {name: 'Add share'}).click();
    const dlg = form(page);
    await dlg.getByRole('radio', {name: /NFS/}).check();
    await expect(dlg.locator('[data-field="user"]')).toHaveCount(0);
    await dlg.locator('[data-field="server"]').fill('nas.lan');
    await dlg.locator('[data-field="path"]').fill('volume1/data/');
    await dlg.locator('[data-field="read-only"]').check();
    await dlg.locator('[data-field="name"]').fill('nas');
    await dlg.getByRole('button', {name: 'Save'}).click();
    await expect(dlg.locator('[data-form-error]')).toContainText('a share named nas exists');
    await dlg.locator('[data-field="name"]').fill('data');
    const [req] = await Promise.all([
        page.waitForRequest((r) => r.method() === 'POST' && r.url().endsWith('/storage/shares')),
        dlg.getByRole('button', {name: 'Save'}).click(),
    ]);
    expect(req.postDataJSON()).toEqual({id: 'data', kind: 'nfs', server: 'nas.lan', path: '/volume1/data', version: '', read_only: true});
    await expect(share(page, 'data').locator('.ol-card-sub')).toHaveText('nas.lan:/volume1/data');
});

test('edit: the name and the kind are fixed, the password stays unless typed', async ({page, baseURL}) => {
    await own(page, baseURL);
    await share(page, 'nas').getByRole('button', {name: 'Edit'}).click();
    const dlg = form(page);
    await expect(dlg).toContainText('Edit share nas');
    await expect(dlg.locator('[data-field="name"]')).toBeDisabled();
    await expect(dlg.getByRole('radio', {name: /NFS/})).toBeDisabled();
    await expect(dlg.locator('[data-field="password"]')).toHaveAttribute('placeholder', '(unchanged)');
    await dlg.locator('[data-field="seal"]').check();
    const [req] = await Promise.all([
        page.waitForRequest((r) => r.method() === 'PUT' && r.url().endsWith('/storage/shares/nas')),
        dlg.getByRole('button', {name: 'Save'}).click(),
    ]);
    expect(req.postDataJSON()).toEqual({id: 'nas', kind: 'cifs', server: 'nas.lan', path: 'backup', version: '', read_only: false, seal: true, user: 'ccu'});
});

test('Test, Mount, Unmount and a server that is down', async ({page, baseURL}) => {
    await own(page, baseURL);
    await share(page, 'nas').getByRole('button', {name: 'Test'}).click();
    await expect(share(page, 'nas').locator('[data-test-result="writable"]')).toContainText('Writable: 1 MB written, read back and deleted. About 38.4 MB/s.');
    await share(page, 'media').getByRole('button', {name: 'Test'}).click();
    await expect(share(page, 'media').locator('[data-test-result="readable"]')).toContainText('mounted read-only, so nothing was written');
    await share(page, 'media').getByRole('button', {name: 'Mount'}).click();
    await expect(share(page, 'media').locator('[data-state]')).toHaveText('mounted');
    await share(page, 'media').getByRole('button', {name: 'Unmount'}).click();
    await expect(share(page, 'media').locator('[data-state]')).toHaveText('mounted when used');
    // a share whose server is down: the test and the mount say so
    await share(page, 'media').getByRole('button', {name: 'Edit'}).click();
    await form(page).locator('[data-field="server"]').fill('down.lan');
    await form(page).getByRole('button', {name: 'Save'}).click();
    await share(page, 'media').getByRole('button', {name: 'Test'}).click();
    await expect(share(page, 'media').locator('[data-test-result="unreachable"]')).toContainText('unreachable (mount): mount error(113)');
    await share(page, 'media').getByRole('button', {name: 'Mount'}).click();
    await expect(share(page, 'media').locator('[data-state]')).toHaveText('unreachable');
    await expect(share(page, 'media').locator('[data-detail]')).toContainText('could not connect to down.lan');
});

test('remove: one danger click, the files stay', async ({page, baseURL}) => {
    await own(page, baseURL);
    await share(page, 'media').getByRole('button', {name: 'Remove'}).click();
    const dialog = page.getByRole('dialog');
    await expect(dialog).toContainText('the files on the server stay where they are');
    await dialog.getByRole('button', {name: 'Remove'}).click();
    await expect(share(page, 'media')).toHaveCount(0);
    await expect(share(page, 'nas')).toBeVisible();
});

test('none, a container, a user without the buttons, German', async ({page, baseURL}) => {
    await own(page, baseURL, 'none');
    await expect(page.locator('[data-shares-empty]')).toHaveText('No network share yet.');
    await expect(page.getByRole('button', {name: 'Add share'})).toBeVisible();
    await own(page, baseURL, 'container');
    await expect(page.locator('[data-notice="shares-container"]')).toContainText('This system is a container');
    await expect(page.getByRole('button', {name: 'Add share'})).toHaveCount(0);
    await page.route('**/api/auth/v1/state', (r) => r.fulfill({json: {setup_required: false, authenticated: true, user: 'monitor', role: 'user', must_change_password: false, sid: 'U1'}}));
    await own(page, baseURL);
    await expect(share(page, 'nas')).toBeVisible();
    await expect(page.getByRole('button', {name: 'Test'})).toHaveCount(0);
    await expect(page.getByRole('button', {name: 'Add share'})).toHaveCount(0);
    await page.unroute('**/api/auth/v1/state');
    await page.addInitScript(() => localStorage.setItem('ol.language', 'de'));
    await own(page, baseURL);
    await expect(page.getByRole('heading', {level: 2, name: 'Netzwerkfreigaben'})).toBeVisible();
    await expect(share(page, 'nas').locator('[data-state]')).toHaveText('eingehängt');
    await expect(page.getByRole('button', {name: 'Freigabe hinzufügen'})).toBeVisible();
    expect(await fitsWindow(page)).toBe(true);
});

// phase 3: what uses a share, with links there; a share in use is not removed
test('a share in use: its uses linked, Remove waits', async ({page, baseURL}) => {
    await own(page, baseURL, 'used');
    const nas = share(page, 'nas');
    await expect(nas.locator('[data-uses]')).toHaveText("In use by: the journal's copies, the backup target TrueNAS");
    await expect(nas.locator('[data-uses]').getByRole('link', {name: 'the backup target TrueNAS'})).toHaveAttribute('href', '/system/backup#targets');
    await expect(nas.locator('[data-uses]').getByRole('link', {name: "the journal's copies"})).toHaveAttribute('href', '/system/log?settings=journal');
    await expect(nas.getByRole('button', {name: 'Remove'})).toBeDisabled();
    await expect(share(page, 'media').getByRole('button', {name: 'Remove'})).toBeEnabled();
});

// task 241 (the maintainer: "use the same concept like everywhere else"): Add share and Edit open an
// in-page panel under the heading, as Add gateway does - no dialog; it opens and closes again from
// its button, Cancel closes it, Save closes it; German, phone width.
test('Add share and Edit are an in-page panel, not a dialog', async ({page, baseURL}) => {
    await own(page, baseURL);
    const addBtn = page.getByRole('button', {name: 'Add share'});
    await expect(addBtn).toHaveAttribute('aria-expanded', 'false');
    await addBtn.click();
    const panel = form(page);
    await expect(panel).toBeVisible();
    await expect(page.getByRole('dialog')).toHaveCount(0);
    await expect(panel.getByRole('heading', {name: 'Add a network share'})).toBeVisible();
    await expect(addBtn).toHaveAttribute('aria-expanded', 'true');
    // under the heading, above the shares
    const y = async (l: ReturnType<Page['locator']>) => (await l.boundingBox())!.y;
    expect(await y(panel)).toBeLessThan(await y(share(page, 'nas')));
    expect(await y(panel)).toBeGreaterThan(await y(page.getByRole('heading', {level: 2, name: 'Network shares'})));
    // the button closes it again, Cancel too
    await addBtn.click();
    await expect(panel).toHaveCount(0);
    await addBtn.click();
    await expect(panel).toBeVisible();
    await panel.getByRole('button', {name: 'Cancel'}).click();
    await expect(panel).toHaveCount(0);
    // Edit: the same panel for that share; a second click closes it; another share's Edit switches it
    await share(page, 'nas').getByRole('button', {name: 'Edit'}).click();
    await expect(panel.getByRole('heading', {name: 'Edit share nas'})).toBeVisible();
    await expect(share(page, 'nas').getByRole('button', {name: 'Edit'})).toHaveAttribute('aria-expanded', 'true');
    await share(page, 'media').getByRole('button', {name: 'Edit'}).click();
    await expect(panel.getByRole('heading', {name: 'Edit share media'})).toBeVisible();
    await expect(panel.locator('[data-field="server"]')).toHaveValue('192.168.1.20');
    await share(page, 'media').getByRole('button', {name: 'Edit'}).click();
    await expect(panel).toHaveCount(0);
    // Save closes it
    await share(page, 'media').getByRole('button', {name: 'Edit'}).click();
    await panel.locator('[data-field="server"]').fill('192.168.1.21');
    await panel.getByRole('button', {name: 'Save'}).click();
    await expect(panel).toHaveCount(0);
    await expect(share(page, 'media').locator('.ol-card-sub')).toHaveText('192.168.1.21:/mnt/tank/media');
    expect(await fitsWindow(page)).toBe(true);
});

test('the share panel in German', async ({page, baseURL}) => {
    await page.addInitScript(() => localStorage.setItem('ol.language', 'de'));
    await own(page, baseURL);
    await page.getByRole('button', {name: 'Freigabe hinzufügen'}).click();
    const panel = form(page);
    await expect(panel.getByRole('heading', {name: 'Netzwerkfreigabe hinzufügen'})).toBeVisible();
    await expect(panel.getByRole('button', {name: 'Abbrechen'})).toBeVisible();
    await expect(panel.getByRole('button', {name: 'Speichern'})).toBeVisible();
    expect(await fitsWindow(page)).toBe(true);
});
