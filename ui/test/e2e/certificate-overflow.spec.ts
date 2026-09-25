import {expect, test} from '@playwright/test';

// B-58: the Certificate page's cards with a long issuer DN (the stub's is over 200 characters,
// as a step-ca hands them out) and the SHA-256 fingerprint. Nothing may run past a card's edge
// on the desktop or the phone: every card's scrollWidth is its clientWidth, the long values are
// wrapped small, and the full text is in the title.
test('no card overflows with a long issuer and a fingerprint', async ({page}) => {
    await page.goto('/system/certificates');
    const cards = page.locator('.ol-cards').first().locator('.ol-card');
    await expect(cards).toHaveCount(5);
    const issuer = cards.filter({hasText: 'Issuer'}).locator('.v');
    await expect(issuer).toContainText('step-ca.lan.example.org');
    // B-74: the common name and the organisation, not the DN with its hex-encoded e-mail attribute
    await expect(issuer).toHaveText('step-ca.lan.example.org Intermediate CA · an-organisation-with-a-deliberately-long-name-for-the-card');
    await expect(issuer).not.toContainText('1.2.840.113549');
    const sizes = await cards.evaluateAll((els) => els.map((e) => ({scroll: e.scrollWidth, client: e.clientWidth, right: e.getBoundingClientRect().right, vw: document.documentElement.clientWidth})));
    for (const s of sizes) {
        expect(s.scroll).toBeLessThanOrEqual(s.client);
        expect(s.right).toBeLessThanOrEqual(s.vw);
    }
    // the value boxes too: the DN wraps inside the card, it does not slide under the neighbour
    const inner = await cards.locator('.v').evaluateAll((els) => els.map((e) => ({scroll: e.scrollWidth, client: e.clientWidth})));
    for (const s of inner) expect(s.scroll).toBeLessThanOrEqual(s.client);
    // the full text is the title, on the DN and on the fingerprint
    await expect(issuer).toHaveAttribute('title', /^CN=step-ca\.lan\.example\.org\+Intermediate\+CA,OU=lan\.example\.org,O=.{150,}/);
    const fp = cards.filter({hasText: 'Fingerprint'}).locator('.v');
    await expect(fp).toHaveAttribute('title', /^([0-9A-F]{2}:){31}[0-9A-F]{2}$/);
    // small, and clamped: the issuer takes at most five lines
    const lines = await issuer.evaluate((e) => Math.round(e.getBoundingClientRect().height / parseFloat(getComputedStyle(e).lineHeight)));
    expect(lines).toBeLessThanOrEqual(5);
    expect(await issuer.evaluate((e) => parseFloat(getComputedStyle(e).fontSize))).toBeLessThanOrEqual(12);
});

// the maintainer, 2026-09-19: on a phone the form's labels sit above their fields, both starting at
// the same edge; on a desktop the label keeps its own column beside the field
test('the form stacks its labels above the fields on a phone', async ({page}, info) => {
    await page.goto('/system/certificates');
    await page.getByRole('radio', {name: 'ACME'}).check();
    const label = page.locator('.ol-certform label', {hasText: 'E-mail'}).locator('span').first();
    const field = page.locator('.ol-certform label', {hasText: 'E-mail'}).locator('input');
    const [l, f] = [await label.boundingBox(), await field.boundingBox()];
    if (info.project.name === 'phone') {
        expect(l!.y + l!.height).toBeLessThanOrEqual(f!.y + 1);
        expect(Math.abs(l!.x - f!.x)).toBeLessThan(2);
    } else {
        expect(l!.y).toBeGreaterThan(f!.y - f!.height);
        expect(f!.x).toBeGreaterThan(l!.x + l!.width);
    }
});
