import {expect, test, type Locator, type Page} from '@playwright/test';
import {fitsWindow} from './scroll';

// B-156: a filter narrows a table's rows, not its columns - the header cells keep the widths they
// had when the filter started, and get their natural widths back when it is cleared.

async function widths(table: Locator): Promise<number[]> {
    return table.locator('thead tr').first().locator('th').evaluateAll((ths) => ths.map((th) => th.getBoundingClientRect().width));
}
function near(a: number[], b: number[]) {
    expect(a.length).toBe(b.length);
    a.forEach((w, i) => expect(Math.abs(w - (b[i] ?? 0)), `column ${i}: ${w} vs ${b[i]}`).toBeLessThanOrEqual(1));
}

const cases: {name: string; path: string; table: string; input: (p: Page) => Locator; query: string}[] = [
    {name: 'Services', path: '/system/services', table: 'table.sv-table:not(.ol-timers)', input: (p) => p.locator('.sv-filter input, input[placeholder="Filter"]').first(), query: 'rfd'},
    {name: 'Firewall rules', path: '/system/firewall', table: 'table.fw-rules', input: (p) => p.getByRole('textbox', {name: 'Filter the rules'}), query: 'ssh'},
    {name: 'Firewall listeners', path: '/system/firewall', table: 'table.fw-listeners', input: (p) => p.getByRole('textbox', {name: 'Filter the listeners'}), query: 'sshd'},
];

for (const c of cases) {
    test(`${c.name}: the columns keep their widths while filtering`, async ({page}, info) => {
        test.skip(info.project.name === 'phone', 'stacked or scrolled on a phone: no column widths to hold');
        await page.goto(c.path);
        const table = page.locator(c.table).first();
        const rows = table.locator('tbody tr');
        await expect(rows.first()).toBeVisible();
        const all = await rows.count();
        // the recorded widths follow the last layout; give it a frame
        await page.evaluate(() => new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r))));
        const before = await widths(table);
        await c.input(page).fill(c.query);
        await expect.poll(() => rows.count()).toBeLessThan(all);
        expect(await rows.count()).toBeGreaterThan(0);
        near(await widths(table), before);
        await c.input(page).fill('');
        await expect.poll(() => rows.count()).toBe(all);
        near(await widths(table), before);
    });
}

test('the filtered pages fit the window', async ({page}) => {
    for (const c of cases) {
        await page.goto(c.path);
        await expect(page.locator(c.table).first().locator('tbody tr').first()).toBeAttached();
        await c.input(page).fill(c.query);
        expect(await fitsWindow(page), c.name).toBe(true);
    }
});
