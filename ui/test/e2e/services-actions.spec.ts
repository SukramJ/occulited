import {type Locator, type Page} from '@playwright/test';
import {expect, test} from './fixtures';

// Task 87: the Services page's row actions. Every text button of the services table and of the timers
// table has the same width (one sizer for both), both tables use the same grid, and the action cells
// stand at the right edge - so a timer's Log and ⋯ are exactly under a service's. Each button has an
// icon before its label, the ⋯ menu's items have theirs, and below B-75's 1100 px step the buttons
// are icon squares in one row, named by aria-label and title.

async function open(page: Page, language: 'en' | 'de', width: number) {
    await page.addInitScript((l) => {
        localStorage.setItem('ol.language', l);
        localStorage.setItem('ol.services.hideSystem', '0');
        localStorage.setItem('ol.services.hideOccu', '0');
    }, language);
    await page.setViewportSize({width, height: 900});
    await page.goto('/system/services');
    await expect(services(page).locator('tbody tr').first()).toBeVisible();
    await expect(timers(page).locator('tbody tr').first()).toBeVisible();
}

const services = (page: Page) => page.locator('table.ol-table').first();
const timers = (page: Page) => page.locator('table.ol-timers');
const rfd = (page: Page) => services(page).locator('tbody tr').filter({has: page.locator('.sv-id', {hasText: /^rfd$/})});

async function boxes(list: Locator) {
    const out: {x: number; y: number; width: number; height: number}[] = [];
    for (const el of await list.all()) out.push((await el.boundingBox())!);
    return out;
}

const LABELS = {
    en: {stop: 'Stop', restart: 'Restart', log: 'Log', run: 'Run now', more: 'More actions', edit: 'Edit unit…'},
    de: {stop: 'Stoppen', restart: 'Neu starten', log: 'Protokoll', run: 'Ausführen', more: 'Weitere Aktionen', edit: 'Unit bearbeiten…'},
};

test.describe('the row actions on a desktop', () => {
    test.skip(({isMobile}) => isMobile, 'the widths of a desktop window; the phone has the icon squares below');

    for (const language of ['en', 'de'] as const) {
        for (const width of [1920, 1280]) {
            test(`one width for every text button and Log and ⋯ under each other, ${width} px, ${language}`, async ({page}) => {
                await open(page, language, width);
                const text = await boxes(page.locator('table.ol-table .ol-rowactions .ol-act:not(.ol-morebtn)'));
                expect(text.length).toBeGreaterThan(10);
                const widths = text.map((b) => b.width);
                expect(Math.max(...widths) - Math.min(...widths), `widths ${[...new Set(widths)].join(', ')}`).toBeLessThanOrEqual(0.5);

                // a service row with every action and a timer row: Log and the menu at the same x, and
                // the first button too
                const service = rfd(page).locator('.ol-rowactions');
                const timer = timers(page).locator('tbody tr').first().locator('.ol-rowactions');
                const x = async (l: Locator) => (await l.boundingBox())!.x;
                expect(Math.abs((await x(service.locator('.ol-logbtn'))) - (await x(timer.locator('.ol-logbtn'))))).toBeLessThanOrEqual(0.5);
                expect(Math.abs((await x(service.locator('.ol-morebtn'))) - (await x(timer.locator('.ol-morebtn'))))).toBeLessThanOrEqual(0.5);
                expect(Math.abs((await x(service.locator('.ol-act').first())) - (await x(timer.locator('.ol-act').first())))).toBeLessThanOrEqual(0.5);
            });
        }

        test(`each button shows its icon and label, each menu item its icon, ${language}`, async ({page}) => {
            const l = LABELS[language];
            await open(page, language, 1600);
            const row = rfd(page);
            for (const [name, label] of [['stop', l.stop], ['restart', l.restart], ['log', l.log]] as const) {
                const button = row.locator('.ol-act', {has: page.locator(`.ol-acttext:text-is("${label}")`)});
                await expect(button, name).toHaveCount(1);
                await expect(button.locator('.ol-acttext')).toBeVisible();
                await expect(button.locator('svg.ol-icon')).toBeVisible();
                await expect(button.locator('svg.ol-icon')).toHaveAttribute('aria-hidden', 'true');
                await expect(button).toHaveAccessibleName(label);
            }
            const run = timers(page).locator('tbody tr').first().locator('.ol-act').first();
            await expect(run.locator('.ol-acttext')).toHaveText(l.run);
            await expect(run.locator('svg.ol-icon')).toBeVisible();

            const more = row.getByRole('button', {name: l.more});
            await expect(more.locator('svg.ol-icon')).toBeVisible();
            await expect(more).toHaveText('');
            await more.click({force: true});
            const items = page.getByRole('menuitem');
            expect(await items.count()).toBeGreaterThan(0);
            for (const item of await items.all()) await expect(item.locator('svg.ol-icon')).toBeVisible();
            await expect(page.getByRole('menuitem', {name: l.edit})).toBeVisible();
        });
    }

    test('Run as root carries the warning colour on its icon, Own user the plain one', async ({page}) => {
        // one addon confined (it has its own user, so its menu offers root), the others root
        await page.route('**/api/system/v1/services', async (route) => {
            if (route.request().method() !== 'GET') return route.fallback();
            const response = await route.fetch();
            const body = (await response.json()) as {services: Record<string, unknown>[]};
            Object.assign(body.services.find((s) => s.id === 'addon-mosquitto')!, {user: 'addon-mosquitto'});
            await route.fulfill({response, json: body});
        });
        await open(page, 'en', 1600);
        const row = (id: string) => services(page).locator('tbody tr').filter({has: page.locator('.sv-id', {hasText: new RegExp(`^${id}$`)})});
        const colour = (item: Locator) => item.locator('.sv-menuicon').evaluate((el) => getComputedStyle(el).color);
        const token = (name: string) =>
            page.evaluate((n) => {
                const probe = document.createElement('span');
                probe.style.color = `var(${n})`;
                document.body.append(probe);
                const c = getComputedStyle(probe).color;
                probe.remove();
                return c;
            }, name);

        await row('addon-mosquitto').getByRole('button', {name: 'More actions'}).click({force: true});
        const root = page.getByRole('menuitem', {name: 'Run as root (unsafe)'});
        await expect(root.locator('svg.ol-icon')).toBeVisible();
        expect(await colour(root)).toBe(await token('--hmm-warn'));
        await page.keyboard.press('Escape');

        await row('addon-mh').getByRole('button', {name: 'More actions'}).click({force: true});
        const own = page.getByRole('menuitem', {name: 'Own user'});
        await expect(own.locator('svg.ol-icon')).toBeVisible();
        expect(await colour(own)).toBe(await token('--hmm-fg-muted'));
    });

    test('screenshots of both tables', async ({page}, info) => {
        await open(page, 'en', 1440);
        await rfd(page).scrollIntoViewIfNeeded();
        await info.attach(`services-actions-${info.project.name}`, {body: await page.screenshot({fullPage: true}), contentType: 'image/png'});
        const dir = process.env.T87_SHOTS;
        if (dir) await page.screenshot({path: `${dir}/services-actions-${info.project.name}.png`, fullPage: true});
    });
});

test.describe('the row actions below 1100 px', () => {
    for (const language of ['en', 'de'] as const) {
        test(`icon squares in one row, named by aria-label and title, ${language}`, async ({page}, info) => {
            const l = LABELS[language];
            await open(page, language, 768);
            for (const grid of [rfd(page).locator('.ol-rowactions'), timers(page).locator('tbody tr').first().locator('.ol-rowactions')]) {
                const acts = grid.locator('.ol-act');
                const all = await boxes(acts);
                expect(all.length).toBeGreaterThanOrEqual(3);
                for (const b of all) {
                    expect(Math.abs(b.width - 28)).toBeLessThanOrEqual(0.5);
                    expect(Math.abs(b.height - 28)).toBeLessThanOrEqual(0.5);
                    expect(Math.abs(b.y - all[0]!.y), 'one row').toBeLessThanOrEqual(0.5);
                }
                for (const el of await acts.all()) {
                    await expect(el.locator('.ol-acttext')).toBeHidden();
                    await expect(el.locator('svg.ol-icon')).toBeVisible();
                    expect(await el.getAttribute('aria-label')).toBeTruthy();
                    expect(await el.getAttribute('title')).toBeTruthy();
                }
            }
            const row = rfd(page);
            for (const label of [l.stop, l.restart, l.more]) {
                const button = row.getByRole('button', {name: label, exact: true});
                await expect(button).toBeVisible();
                await expect(button).toHaveAttribute('title', label);
            }
            const log = row.getByRole('link', {name: l.log, exact: true});
            await expect(log).toBeVisible();
            expect((await log.getAttribute('title'))?.toLowerCase()).toContain(l.log.toLowerCase());
            const run = timers(page).locator('tbody tr').first().getByRole('button', {name: l.run, exact: true});
            await expect(run).toHaveAttribute('title', l.run);
            if (language === 'en') {
                const dir = process.env.T87_SHOTS;
                if (dir) await page.screenshot({path: `${dir}/services-actions-768-${info.project.name}.png`, fullPage: true});
            }
        });
    }

    test('the focus ring stays on an icon square', async ({page}) => {
        await open(page, 'en', 768);
        const restart = rfd(page).getByRole('button', {name: 'Restart', exact: true});
        await restart.focus();
        await page.keyboard.press('Shift+Tab');
        await page.keyboard.press('Tab');
        await expect(restart).toBeFocused();
        const outline = await restart.evaluate((el) => getComputedStyle(el).outlineStyle);
        expect(outline).not.toBe('none');
    });
});
