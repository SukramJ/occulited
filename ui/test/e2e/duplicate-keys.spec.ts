import {expect, test, type Page} from '@playwright/test';

// B-80: two lists the box can report with a repeated name, as the Charly did. The Interfaces page
// keyed its subscribers by the id and the Network page its listening ports by protocol and port;
// both threw each_key_duplicate and Interfaces stayed at Loading. The stub carries both cases:
// Homematic Manager's old and new registration (one id, two callbacks) and one port on several
// addresses.

function collectErrors(page: Page): string[] {
    const errors: string[] = [];
    page.on('pageerror', (e) => errors.push(`pageerror: ${e.message}`));
    page.on('console', (m) => {
        if (m.type() === 'error') errors.push(`console: ${m.text()}`);
    });
    return errors;
}

test('a client registered twice is two subscriber rows and the Interfaces page renders', async ({page}) => {
    const errors = collectErrors(page);
    await page.goto('/radio');
    await expect(page.locator('[data-process="rfd"]')).toBeVisible();
    // the rows of clients on this system are on the Interfaces page
    const card = page.locator('.ol-process[data-interface="VirtualDevices"]');
    await expect(card.locator('.ol-sub-count')).toHaveText('2 subscribers');
    const rows = card.locator('li.ol-sub', {hasText: 'hmm_VirtualDevices'});
    await expect(rows).toHaveCount(2);
    await expect(rows.nth(0)).toContainText('xmlrpc://127.0.0.1:2140');
    await expect(rows.nth(1)).toContainText('xmlrpc://127.0.0.1:2141');
    // task 76: which of the two is stale is not knowable from the file, so both are marked
    await expect(rows.locator('.ol-badge.warn')).toHaveText(['duplicate registration', 'duplicate registration']);
    expect(errors, errors.join('\n')).toEqual([]);
});

test('a port bound on several addresses is a row per address and the Firewall page renders', async ({page}) => {
    const errors = collectErrors(page);
    // task 57: the listening ports are on the Firewall page, split out of Network
    await page.goto('/system/firewall');
    const listening = page.locator('table.fw-listeners');
    await expect(listening).toBeVisible();
    const node = listening.locator('tbody tr', {hasText: '1880'});
    await expect(node).toHaveCount(3);
    await expect(node.nth(0)).toContainText('0.0.0.0');
    await expect(node.nth(1)).toContainText('127.0.0.1');
    await expect(node.nth(2)).toContainText('::');
    await expect(listening.locator('tbody tr', {hasText: '5353'})).toHaveCount(2);
    expect(errors, errors.join('\n')).toEqual([]);
});
