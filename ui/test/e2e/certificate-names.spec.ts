import {expect, test} from './fixtures';

// With no ACME names stored, the Certificate page proposes the box's FQDN and its host name, marked
// as the proposal; an ACME save with the field left empty stores exactly those.

test('the page suggests the host name and the domain while no names are stored', async ({page}) => {
    await page.goto('/system/certificates');
    await page.getByRole('radio', {name: 'ACME'}).check();
    const names = page.locator('#ol-cert-names');
    await expect(names).toHaveValue('');
    await expect(names).toHaveAttribute('placeholder', 'openccu.home.arpa, openccu');
    const hint = page.locator('[data-suggested]');
    await expect(hint).toHaveText('suggested from the host name and the domain: openccu.home.arpa, openccu');
    await expect(hint).toHaveClass(/ol-muted/);
    // a typed name is not a proposal any more
    await names.fill('ccu.example.org');
    await expect(hint).toHaveCount(0);
    await names.fill('');
    await expect(hint).toBeVisible();
});

test('an ACME save with the names left empty stores the suggestion', async ({page}) => {
    await page.goto('/system/certificates');
    await page.getByRole('radio', {name: 'ACME'}).check();
    const [req] = await Promise.all([
        page.waitForRequest((r) => r.method() === 'PUT' && r.url().includes('/api/system/v1/certificate/settings')),
        page.locator('.cert-actions').getByRole('button', {name: 'Save'}).click(),
    ]);
    expect(req.postDataJSON().names).toEqual(['openccu.home.arpa', 'openccu']);
    await expect(page.locator('#ol-cert-names')).toHaveValue('openccu.home.arpa, openccu');
    await expect(page.locator('[data-suggested]')).toHaveCount(0);
});

test('without a domain the suggestion is the host name alone', async ({page}) => {
    await page.route('**/api/system/v1/certificate', async (route) => {
        const response = await route.fetch();
        await route.fulfill({response, json: {...(await response.json()), suggested_names: ['openccu']}});
    });
    await page.goto('/system/certificates');
    await page.getByRole('radio', {name: 'ACME'}).check();
    await expect(page.locator('[data-suggested]')).toHaveText('suggested from the host name: openccu');
    await expect(page.locator('#ol-cert-names')).toHaveAttribute('placeholder', 'openccu');
});
