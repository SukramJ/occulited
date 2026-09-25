import {test, expect} from '@playwright/test';

// task 104: on a phone the filters' second row is folded behind a Filters toggle; this test uses its
// Tag filter, so it starts with the fold open (the page remembers it per browser)
test.beforeEach(async ({page}) => {
    await page.addInitScript(() => localStorage.setItem('ol.log.filters', 'open'));
});

// hmipserver logs to the journal: the Tag filter names it HmIP server, finds it by its identifier
// too, and choosing it asks for tag=hmipserver and shows its lines alone
test('the HmIP server source in the Tag filter filters on hmipserver', async ({page}) => {
    const reqs: string[] = [];
    page.on('request', (r) => {
        if (r.url().includes('/api/system/v1/log?')) reqs.push(new URL(r.url()).search);
    });
    await page.goto('/system/log');
    await expect(page.locator('.ol-line').first()).toBeVisible();
    const tag = page.getByRole('combobox', {name: 'Tag'});
    await tag.click();
    const list = page.getByRole('listbox', {name: 'Tag'});
    await expect(list.getByRole('option', {name: 'HmIP server', exact: true})).toBeVisible();
    await expect(list.getByRole('option', {name: 'hmipserver', exact: true})).toHaveCount(0);
    await page.getByRole('searchbox', {name: 'Filter'}).fill('hmipserver');
    await expect(list.getByRole('option')).toHaveText(['HmIP server']);
    await list.getByRole('option', {name: 'HmIP server', exact: true}).click();
    await expect.poll(() => reqs.at(-1)).toContain('tag=hmipserver');
    await expect(tag).toContainText('Tag: HmIP server');
    await expect
        .poll(async () => {
            const src = await page.locator('.ol-line .ol-src').allTextContents();
            return src.length > 0 && src.every((s) => /^hmipserver\[\d+\]$/.test(s));
        })
        .toBe(true);
    // the other tags are not labelled: rfd stays rfd
    await tag.click();
    await expect(list.getByRole('option', {name: 'rfd', exact: true})).toBeVisible();
});
