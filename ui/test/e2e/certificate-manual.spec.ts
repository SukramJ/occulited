import {expect, test} from '@playwright/test';
import {CERT, KEY, CA, OTHER_KEY} from '../stub/testcert.mjs';

// Task 38: mode manual on the Certificate page - a pasted certificate and key are parsed into a
// preview under each tall PEM field, Install is one PUT of the three PEM strings, and the page
// shows the installed certificate; a key and CSR made on the box, the CSR downloaded.
test('paste a certificate and its key: preview, then install', async ({page}) => {
    await page.goto('/system/certificates');
    await page.getByRole('radio', {name: 'Manual'}).check();
    const cert = page.getByLabel('Certificate (PEM)');
    const key = page.getByLabel('Private key (PEM)');
    await expect(cert).toBeVisible();
    // every PEM field is tall: 30 rows, and at least 30 lines high on the screen
    for (const ta of await page.locator('textarea.pem').all()) {
        await expect(ta).toHaveAttribute('rows', '30');
        const h = await ta.evaluate((e) => e.getBoundingClientRect().height / parseFloat(getComputedStyle(e).lineHeight));
        expect(h).toBeGreaterThanOrEqual(29);
    }
    await cert.fill(CERT);
    const pvCert = page.locator('[data-preview="certificate"]').first();
    await expect(pvCert).toContainText('CN=ccu.example.org');
    await expect(pvCert).toContainText('issued by');
    // B-74: the issuer by its common name and organisation, the DN behind the pointer
    await expect(pvCert).toContainText('issued by Stub Test CA · openccu-lite tests');
    await expect(pvCert.locator('.pv-sub[title*="CN=Stub Test CA"]')).toHaveText('Stub Test CA · openccu-lite tests');
    await expect(pvCert).toContainText('lines');
    // a leaf without its issuer: the chain warning, not a refusal
    await expect(page.locator('[data-preview="chain"]')).toContainText('empty');
    // the wrong key says so, the right one too
    await key.fill(OTHER_KEY);
    const pvKey = page.locator('[data-preview="key"]');
    await expect(pvKey).toContainText('does not belong to the certificate');
    await key.fill(KEY);
    await expect(pvKey).toContainText('belongs to the certificate');
    await expect(pvKey).not.toContainText('does not belong');
    await expect(pvKey).toContainText('P-256');
    // the chain: the CA, and the warning is gone
    await page.getByLabel('Chain (PEM)').fill(CA);
    await expect(page.locator('[data-preview="chain"]')).toContainText('2 certificates');
    // Install: one PUT with the three PEM strings
    const [req] = await Promise.all([
        page.waitForRequest((r) => r.method() === 'PUT' && r.url().endsWith('/api/system/v1/certificate/manual')),
        page.getByRole('button', {name: 'Install'}).click(),
    ]);
    const body = req.postDataJSON();
    expect(body.certificate).toBe(CERT);
    expect(body.key).toBe(KEY);
    expect(body.chain).toBe(CA);
    await expect(page.getByText(/^Installed: /)).toBeVisible();
    await expect(page.getByText(/Restarted: mosquitto/)).toBeVisible();
    const cards = page.locator('.ol-cards').first();
    await expect(cards).toContainText('manual, installed by hand');
    await expect(cards).toContainText('ccu.example.org');
    await expect(cards).toContainText('Stub Test CA');
    // the fields are empty again, the mode stays manual
    await expect(cert).toHaveValue('');
    await expect(page.getByRole('radio', {name: 'Manual'})).toBeChecked();
});

test('a key and request made on the box, the CSR downloaded', async ({page}) => {
    await page.goto('/system/certificates');
    await page.getByRole('radio', {name: 'Manual'}).check();
    await expect(page.getByRole('heading', {level: 2, name: 'Key and certificate request'})).toBeVisible();
    const cn = page.getByPlaceholder('ccu.example.org').last();
    await cn.fill('ccu.example.org');
    await page.getByPlaceholder('ccu.lan, 192.0.2.119').fill('ccu.lan');
    await page.locator('select').last().selectOption('p384');
    const gen = page.getByRole('button', {name: /Generate (a new )?key and request/});
    const reqP = page.waitForRequest((r) => r.method() === 'POST' && r.url().endsWith('/api/system/v1/certificate/key'));
    await gen.click();
    // a pending key from an earlier run (the projects share the stub) is replaced after the question
    const dlg = page.getByRole('dialog');
    if (await dlg.waitFor({timeout: 800}).then(() => true, () => false)) await dlg.getByRole('button', {name: 'Generate anyway'}).click();
    const req = await reqP;
    expect(req.postDataJSON()).toEqual({algorithm: 'p384', cn: 'ccu.example.org', sans: ['ccu.lan'], org: ''});
    await expect(page.getByText(/^Key and request generated/)).toBeVisible();
    const pend = page.locator('.pend');
    await expect(pend).toContainText('Pending request');
    await expect(pend).toContainText('ccu.example.org');
    await expect(pend).toContainText('P-384');
    // the request as text, and as a download with the CN as the file name
    await expect(page.getByLabel('Certificate request (PEM)')).toHaveValue(/^-----BEGIN CERTIFICATE REQUEST-----/);
    const [download] = await Promise.all([page.waitForEvent('download'), page.getByRole('link', {name: 'Download CSR'}).click()]);
    expect(download.suggestedFilename()).toBe('ccu.example.org.csr');
    const stream = await download.createReadStream();
    const chunks: Buffer[] = [];
    for await (const c of stream) chunks.push(Buffer.from(c));
    expect(Buffer.concat(chunks).toString()).toMatch(/^-----BEGIN CERTIFICATE REQUEST-----/);
    // the key field says the box holds the key now
    await expect(page.getByText('the system holds the key of the pending request')).toBeVisible();
});

test("the ACME CA root field is tall and previews the pasted certificate", async ({page}) => {
    await page.goto('/system/certificates');
    await page.getByRole('radio', {name: 'ACME'}).check();
    await page.locator('select').first().selectOption('custom');
    const root = page.locator('textarea.pem');
    await expect(root).toHaveAttribute('rows', '30');
    await root.fill(CA);
    await expect(page.locator('[data-preview="certificate"]')).toContainText('CN=Stub Test CA');
});
