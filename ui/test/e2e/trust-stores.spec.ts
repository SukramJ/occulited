import {expect, test, type Page} from '@playwright/test';
import {fitsWindow} from './scroll';
import {buttonBecomesPanel} from './panels';

// openccu-lite task 231 (the maintainer: "central truststore management page ... in every store one
// should be able to remove and add certificates. pem paste or upload, der upload. user should also
// be able to copy certificate from e.g. systemwide to oidc"): System → Trust stores - four stores,
// add by PEM paste, PEM or DER file, remove (in red where it can break TLS), restore a distrusted
// image certificate, copy between stores, and the strict failure's one-click copy. Task 267: the
// stores are tabs, one shown at a time, the chosen one the URL's anchor, the four tables in one
// column layout; task 268: Add certificate becomes its panel.

const PEM = (n: string) => `-----BEGIN CERTIFICATE-----\nMIIB${n}\n-----END CERTIFICATE-----\n`;

async function own(page: Page, baseURL: string | undefined, pending = false, anchor = '') {
    const cookies = [{name: 'stub-trust', value: `ts-${Math.random().toString(36).slice(2)}`, url: baseURL!}];
    if (pending) cookies.push({name: 'stub-trust-pending', value: '1', url: baseURL!});
    await page.context().addCookies(cookies);
    await page.goto('/system/trust' + anchor);
    await expect(page.locator('[data-trust-store]')).toBeVisible();
}
const store = (page: Page, id: string) => page.locator(`[data-trust-store="${id}"]`);
const tab = (page: Page, id: string) => page.getByRole('tablist', {name: 'Trust stores'}).locator(`[data-tab="${id}"]`);
/** chooses a store's tab and waits for its section */
async function show(page: Page, id: string) {
    await tab(page, id).click();
    await expect(store(page, id)).toBeVisible();
}

test('the four stores, their tables, the menu entry, and nothing wider than the window', async ({page, baseURL}) => {
    await own(page, baseURL);
    await expect(page.getByRole('heading', {level: 1, name: 'System › Trust stores'})).toBeVisible();
    // task 267: four tabs in this order, each with its count; System is shown, the others are not
    const tabs = page.getByRole('tablist', {name: 'Trust stores'}).getByRole('tab');
    await expect(tabs).toHaveCount(4);
    for (const [i, name] of ['System9', 'occulited3', 'OAuth / OIDC0', 'ACME0'].entries()) await expect(tabs.nth(i)).toHaveText(name);
    await expect(tab(page, 'system')).toHaveAttribute('aria-selected', 'true');
    await expect(page.locator('[data-trust-store]')).toHaveCount(1);
    await expect(store(page, 'system').locator('h2')).toContainText('System');
    // the system store: nine rows incl. the distrusted one and the one added by hand, with a filter
    const sys = store(page, 'system');
    await expect(sys.locator('tbody tr')).toHaveCount(9);
    await expect(sys.locator('[data-cert="s000000000000006"] [data-badge="distrusted"]')).toHaveText('distrusted');
    await expect(sys.locator('[data-cert="s000000000000009"] .ts-source')).toContainText('added · by admin');
    await expect(sys.locator('[data-cert="s000000000000001"] .ts-source')).toHaveText('Image');
    await expect(sys.locator('[data-cert="s000000000000001"] [data-fingerprint]')).toHaveText('11:00:00:00:00:00:00:00');
    await expect(sys.locator('[data-cert="s000000000000001"] .ts-subject')).toContainText('ISRG Root X1');
    await sys.getByLabel('Filter the System store').fill('digicert');
    await expect(sys.locator('tbody tr')).toHaveCount(2);
    await expect(sys.locator('.ts-filter')).toContainText('2 of 9 match');
    // occulited's base set of three, no filter under nine; the two anchor stores empty
    await show(page, 'occulited');
    await expect(page).toHaveURL(/\/system\/trust#occulited$/);
    await expect(store(page, 'system')).toHaveCount(0);
    await expect(store(page, 'occulited').locator('tbody tr')).toHaveCount(3);
    await expect(store(page, 'occulited').locator('.ts-filter')).toHaveCount(0);
    await show(page, 'oidc');
    await expect(store(page, 'oidc').locator('[data-trust-empty]')).toContainText('None: the provider');
    // the arrow keys move between the tabs
    await tab(page, 'oidc').press('ArrowRight');
    await expect(store(page, 'acme').locator('[data-trust-empty]')).toContainText('None: the ACME directory');
    await expect(tab(page, 'acme')).toBeFocused();
    await expect(page).toHaveURL(/#acme$/);
    await expect(page.locator('[data-trust-pending]')).toHaveCount(0);
    expect(await fitsWindow(page)).toBe(true);
});

test('the anchor chooses the store; the four tables share their column positions', async ({page, baseURL}) => {
    await own(page, baseURL, false, '#oidc');
    await expect(tab(page, 'oidc')).toHaveAttribute('aria-selected', 'true');
    await expect(store(page, 'oidc')).toBeVisible();
    // fill the two empty stores so that every store has a table
    for (const id of ['oidc', 'acme']) {
        await show(page, id);
        await store(page, id).locator(`[data-trust-add="${id}"]`).click();
        await store(page, id).locator('[data-trust-pem]').fill(PEM(id));
        await store(page, id).locator('[data-action="trust-add"]').click();
        await expect(store(page, id).locator('tbody tr')).toHaveCount(1);
    }
    const columns = async (id: string) => {
        await show(page, id);
        const ths = store(page, id).locator('thead th');
        const out: number[] = [];
        for (let i = 0; i < (await ths.count()); i++) {
            const b = await ths.nth(i).boundingBox();
            out.push(b && b.width > 0 ? Math.round(b.x) : -1);
        }
        return out;
    };
    const first = await columns('system');
    for (const id of ['occulited', 'oidc', 'acme']) expect(await columns(id), id).toEqual(first);
    // a long name ends in an ellipsis and keeps the whole name in its tooltip
    await show(page, 'system');
    const name = store(page, 'system').locator('[data-cert="s000000000000001"] .ts-subject .ts-name');
    await expect(name).toHaveAttribute('title', /ISRG Root X1/);
    if ((page.viewportSize()?.width ?? 0) > 700) await expect(name).toHaveCSS('text-overflow', 'ellipsis');
    expect(await fitsWindow(page)).toBe(true);
});

test('Add certificate becomes its panel and comes back after Cancel', async ({page, baseURL}) => {
    await own(page, baseURL);
    await buttonBecomesPanel(store(page, 'system').locator('[data-trust-add="system"]'), store(page, 'system').getByRole('group', {name: 'Add a certificate to the System store'}));
    await show(page, 'acme');
    await buttonBecomesPanel(store(page, 'acme').locator('[data-trust-add="acme"]'), store(page, 'acme').getByRole('group', {name: 'Add a certificate to the ACME store'}));
});

test('add by PEM paste into ACME: the panel grows from the button, the row appears, the notice says so', async ({page, baseURL}) => {
    await own(page, baseURL);
    await show(page, 'acme');
    const acme = store(page, 'acme');
    await acme.locator('[data-trust-add="acme"]').click();
    await expect(acme.getByRole('group', {name: 'Add a certificate to the ACME store'})).toBeVisible();
    await expect(acme.locator('[data-trust-add="acme"]')).toBeHidden();
    await expect(page.getByRole('dialog')).toHaveCount(0);
    await expect(acme.locator('[data-action="trust-add"]')).toBeDisabled();
    await acme.locator('[data-trust-pem]').fill(PEM('one') + PEM('two'));
    await acme.locator('[data-action="trust-add"]').click();
    await expect(acme.locator('[data-notice="trust-acme"]')).toHaveText('2 certificates are trusted in the ACME store.');
    await expect(acme.locator('tbody tr')).toHaveCount(2);
    await expect(acme.locator('tbody tr').first().locator('.ts-subject')).toContainText('Pasted 1');
    await expect(acme.locator('tbody tr').first().locator('.ts-source')).toContainText('added · by admin');
    // the panel closed, the button back
    await expect(acme.getByRole('group', {name: 'Add a certificate to the ACME store'})).toHaveCount(0);
    await expect(acme.locator('[data-trust-add="acme"]')).toBeVisible();
    // a key is refused
    await acme.locator('[data-trust-add="acme"]').click();
    await acme.locator('[data-trust-pem]').fill('-----BEGIN EC PRIVATE KEY-----\nMHc\n-----END EC PRIVATE KEY-----\n');
    await acme.locator('[data-action="trust-add"]').click();
    await expect(acme.locator('[data-notice="trust-error"]')).toContainText('private key');
});

test('upload a DER file into OAuth / OIDC; a PEM file fills the text', async ({page, baseURL}) => {
    await own(page, baseURL);
    await show(page, 'oidc');
    const oidc = store(page, 'oidc');
    await oidc.locator('[data-trust-add="oidc"]').click();
    await oidc.locator('[data-trust-file]').setInputFiles({name: 'lab-ca.der', mimeType: 'application/pkix-cert', buffer: Buffer.from([0x30, 0x82, 0x01, 0x0a, 0x02, 0x01, 0x01])});
    await expect(oidc.locator('[data-trust-der]')).toHaveText('DER file lab-ca.der');
    await expect(oidc.locator('[data-trust-pem]')).toHaveValue('');
    await oidc.locator('[data-action="trust-add"]').click();
    await expect(oidc.locator('[data-notice="trust-oidc"]')).toHaveText('DER upload 1 is trusted in the OAuth / OIDC store.');
    await expect(oidc.locator('tbody tr')).toHaveCount(1);
    // a PEM file goes into the text field, DER forgotten
    await oidc.locator('[data-trust-add="oidc"]').click();
    await oidc.locator('[data-trust-file]').setInputFiles({name: 'chain.pem', mimeType: 'application/x-pem-file', buffer: Buffer.from(PEM('a'))});
    await expect(oidc.locator('[data-trust-pem]')).toHaveValue(/BEGIN CERTIFICATE/);
    await expect(oidc.locator('[data-trust-der]')).toHaveCount(0);
});

test('copy from System into OAuth / OIDC: asks, copies, the source column says so; the stores are independent', async ({page, baseURL}) => {
    await own(page, baseURL);
    const sys = store(page, 'system');
    const row = sys.locator('[data-cert="s000000000000004"]');
    await row.locator('[data-trust-copy]').selectOption('oidc');
    const dlg = page.getByRole('dialog');
    await expect(dlg).toContainText('DigiCert Global Root G2 (44:00:00:00:00:00:00:00) is copied into the OAuth / OIDC store.');
    await dlg.getByRole('button', {name: 'Copy'}).click();
    await expect(sys.locator('[data-notice="trust-system"]')).toHaveText('DigiCert Global Root G2 is trusted in the OAuth / OIDC store.');
    // the tab's count follows
    await expect(tab(page, 'oidc')).toHaveText('OAuth / OIDC1');
    // the select is back on its placeholder, and offers no copy into the same store
    await expect(row.locator('[data-trust-copy]')).toHaveValue('');
    await expect(row.locator('[data-trust-copy] option[value="system"]')).toHaveCount(0);
    // distrusting it in System leaves the OIDC copy
    await row.locator('[data-action="trust-remove"]').click();
    await page.getByRole('dialog').getByRole('button', {name: 'Remove'}).click();
    await expect(row.locator('[data-badge="distrusted"]')).toBeVisible();
    await show(page, 'oidc');
    const oidcRow = store(page, 'oidc').locator('[data-cert="s000000000000004"]');
    await expect(oidcRow.locator('.ts-source')).toContainText('copied');
    await expect(oidcRow.locator('[data-badge="distrusted"]')).toHaveCount(0);
});

test('remove in System asks in red and distrusts; Trust again brings it back; an anchor asks plainly and goes', async ({page, baseURL}) => {
    await own(page, baseURL);
    const sys = store(page, 'system');
    const row = sys.locator('[data-cert="s000000000000005"]');
    await row.locator('[data-action="trust-remove"]').click();
    const dlg = page.getByRole('dialog');
    await expect(dlg).toContainText('Every program on the system stops trusting GlobalSign Root CA (55:00:00:00:00:00:00:00).');
    await expect(dlg.getByRole('button', {name: 'Remove'})).toHaveClass(/danger/);
    await dlg.getByRole('button', {name: 'Cancel'}).click();
    await expect(row.locator('[data-badge="distrusted"]')).toHaveCount(0);
    await row.locator('[data-action="trust-remove"]').click();
    await page.getByRole('dialog').getByRole('button', {name: 'Remove'}).click();
    await expect(row.locator('[data-badge="distrusted"]')).toHaveText('distrusted');
    await expect(row.locator('[data-action="trust-remove"]')).toHaveCount(0);
    await expect(row.locator('[data-trust-copy]')).toHaveCount(0);
    await expect(sys.locator('tbody tr')).toHaveCount(9);
    await row.locator('[data-action="trust-restore"]').click();
    await expect(sys.locator('[data-notice="trust-system"]')).toHaveText('GlobalSign Root CA is trusted again.');
    await expect(row.locator('[data-badge="distrusted"]')).toHaveCount(0);
    await expect(row.locator('[data-action="trust-remove"]')).toBeVisible();
    // the one added by hand goes for good, still in red (the system store)
    const added = sys.locator('[data-cert="s000000000000009"]');
    await added.locator('[data-action="trust-remove"]').click();
    await expect(page.getByRole('dialog').getByRole('button', {name: 'Remove'})).toHaveClass(/danger/);
    await page.getByRole('dialog').getByRole('button', {name: 'Remove'}).click();
    await expect(sys.locator('tbody tr')).toHaveCount(8);
    // occulited's base set asks in red too; an ACME anchor does not
    await show(page, 'occulited');
    const occ = store(page, 'occulited').locator('[data-cert="s000000000000002"]');
    await occ.locator('[data-action="trust-remove"]').click();
    await expect(page.getByRole('dialog')).toContainText("occulited's own downloads stop trusting USERTrust ECC Certification Authority");
    await expect(page.getByRole('dialog').getByRole('button', {name: 'Remove'})).toHaveClass(/danger/);
    await page.getByRole('dialog').getByRole('button', {name: 'Cancel'}).click();
    await show(page, 'acme');
    const acme = store(page, 'acme');
    await acme.locator('[data-trust-add="acme"]').click();
    await acme.locator('[data-trust-pem]').fill(PEM('x'));
    await acme.locator('[data-action="trust-add"]').click();
    await acme.locator('tbody tr').first().locator('[data-action="trust-remove"]').click();
    await expect(page.getByRole('dialog')).toContainText('is no longer trusted in the ACME store.');
    await expect(page.getByRole('dialog').getByRole('button', {name: 'Remove'})).not.toHaveClass(/danger/);
    await page.getByRole('dialog').getByRole('button', {name: 'Remove'}).click();
    await expect(acme.locator('[data-trust-empty]')).toBeVisible();
});

test('a strict failure: the panel names host and authority, one click copies it from System, the Status warning links here', async ({page, baseURL}) => {
    await own(page, baseURL, true);
    const pending = page.locator('[data-trust-pending="api.github.com"]');
    await expect(pending).toContainText('api.github.com presents a certificate from DigiCert Global Root G2, which the occulited store does not hold.');
    await expect(pending).toContainText('The call fails until the authority is added.');
    await expect(tab(page, 'occulited')).toHaveText('occulited3');
    await pending.locator('[data-action="trust-fix"]').click();
    await expect(page.locator('[data-notice="trust-page"]')).toHaveText('DigiCert Global Root G2 is trusted in the occulited store; api.github.com can be reached again.');
    await expect(page.locator('[data-trust-pending]')).toHaveCount(0);
    await show(page, 'occulited');
    await expect(store(page, 'occulited').locator('[data-cert="s000000000000004"] .ts-source')).toContainText('copied');
    // the Status page's warning, before the fix, in another browser state
    await page.context().clearCookies();
    await page.context().addCookies([{name: 'stub-trust', value: `ts-${Math.random().toString(36).slice(2)}`, url: baseURL!}, {name: 'stub-trust-pending', value: '1', url: baseURL!}]);
    await page.goto('/');
    const warning = page.locator('[data-warnings] [data-notice="trust-ca"]');
    await expect(warning).toContainText('api.github.com: the server presents a certificate from CN=DigiCert Global Root G2,O=DigiCert Inc, which the occulited trust store does not hold.');
    await expect(warning).toContainText('one click on the Trust stores page copies it');
    await expect(warning.getByRole('link', {name: 'Trust stores'})).toHaveAttribute('href', '/system/trust#occulited');
    // the link lands on the occulited store (task 267)
    await warning.getByRole('link', {name: 'Trust stores'}).click();
    await expect(tab(page, 'occulited')).toHaveAttribute('aria-selected', 'true');
    await expect(store(page, 'occulited')).toBeVisible();
});

test('German: the headings and the words of the table', async ({page, baseURL}) => {
    await page.addInitScript(() => localStorage.setItem('ol.language', 'de'));
    await own(page, baseURL);
    await expect(page.getByRole('heading', {level: 1, name: 'System › Vertrauensspeicher'})).toBeVisible();
    await expect(page.locator('[data-trust-store="system"] h2')).toContainText('System');
    await expect(page.getByRole('tablist', {name: 'Vertrauensspeicher'}).getByRole('tab')).toHaveCount(4);
    const sys = store(page, 'system');
    await expect(sys.locator('[data-trust-add="system"]')).toHaveText('Zertifikat hinzufügen');
    await expect(sys.locator('thead')).toContainText('Inhaber');
    await expect(sys.locator('thead')).toContainText('Gültig bis');
    await expect(sys.locator('thead')).toContainText('Herkunft');
    await expect(sys.locator('[data-cert="s000000000000006"] [data-badge="distrusted"]')).toHaveText('nicht vertraut');
    await expect(sys.locator('[data-cert="s000000000000006"] [data-action="trust-restore"]')).toHaveText('Wieder vertrauen');
    await expect(sys.locator('[data-cert="s000000000000001"] [data-trust-copy] option').first()).toHaveText('Kopieren nach…');
    expect(await fitsWindow(page)).toBe(true);
});

// openccu-lite B-246: since task 267's fixed column layout the row's Copy to… dropdown and its
// Remove button broke onto two lines. At desktop widths they share one line, in both languages and
// in every store that has rows; the table still fits the window.
test('B-246: Copy to… and Remove share one line at 1280 px', async ({page, baseURL}) => {
    await page.setViewportSize({width: 1280, height: 900});
    for (const lang of ['en', 'de']) {
        await page.addInitScript((l) => localStorage.setItem('ol.language', l), lang);
        await own(page, baseURL);
        for (const id of ['system', 'occulited']) {
            await page.locator(`[data-tab="${id}"]`).click(); // the tablist's name is the language's; the tab id is not
            await expect(store(page, id)).toBeVisible();
            const row = store(page, id).locator('tbody tr').first().locator('.ts-actrow');
            const copy = row.locator('[data-trust-copy]');
            const remove = row.locator('[data-action="trust-remove"]');
            await expect(copy).toBeVisible();
            await expect(remove).toBeVisible();
            const c = (await copy.boundingBox())!;
            const r = (await remove.boundingBox())!;
            expect(Math.abs(c.y + c.height / 2 - (r.y + r.height / 2)), `${lang} ${id}: same line`).toBeLessThanOrEqual(2);
            expect(r.x, `${lang} ${id}: the button to the right of the dropdown`).toBeGreaterThan(c.x + c.width);
            expect(await fitsWindow(page)).toBe(true);
        }
    }
});
