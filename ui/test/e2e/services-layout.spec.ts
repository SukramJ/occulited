import {expect, test, type Page} from '@playwright/test';
import {PORT} from './scroll';

// B-75: the Services page was wider than the window beyond phones - 1489 px on a 768 px tablet, and
// on a 1280 px desktop in German with every unit shown the action column ran past the right edge.
// It fits a tablet and that desktop now, and at no width in between does a row's action run past
// the window. The fixture gets what a real box lists with both hide boxes off: static units, units
// a condition keeps off, a failed one, an addon outside its unit, long descriptions. On a phone the
// rows stack (B-105); phone-width.spec.ts measures that page.

const EXTRA: [string, string, string, Record<string, unknown>][] = [
    ['emergency', 'system', 'Emergency Shell', {unit_file_state: 'static'}],
    ['rescue', 'system', 'Rescue Shell', {unit_file_state: 'static'}],
    ['initrd-switch-root', 'system', 'Switch Root', {unit_file_state: 'static'}],
    ['plymouth-start', 'system', 'Show Plymouth Boot Screen', {unit_file_state: 'static'}],
    ['qemu-guest-agent', 'system', 'QEMU Guest Agent', {unit_file_state: 'enabled'}],
    ['occu-cron-backup', 'occu', 'Scheduled backup to the userfs', {unit_file_state: 'static', oneshot: false, result: 'success'}],
    ['occu-fstrim', 'occu', 'Discard unused blocks on the userfs', {unit_file_state: 'static'}],
    ['occu-etc-writable', 'occu', 'Writable /etc/passwd and /etc/group for confined addons', {unit_file_state: 'enabled', running: true, oneshot: true, result: 'success'}],
    ['occu-unit-overrides', 'occu', 'Unit overrides and the service switch from the userfs', {unit_file_state: 'enabled', running: true, oneshot: true, result: 'success'}],
    ['occu-syslog-forward', 'occu', 'Forward the system log to a remote syslog host', {unit_file_state: 'enabled'}],
    ['occu-interface-clock', 'occu', 'Set the radio module clock from the system clock', {unit_file_state: 'enabled', failed: true, result: 'exit-code', managed: true}],
    ['occu-usb-gadget', 'occu', 'USB gadget network interface', {unit_file_state: 'masked-runtime', enabled: false}],
    ['hmlangw', 'core', 'LAN gateway mode (hmlangw)', {unit_file_state: 'enabled', skipped: true, managed: true}],
    ['eq3configd', 'core', 'Device configuration daemon (eq3configd)', {unit_file_state: 'enabled', running: true, pid: 1180, memory_bytes: 3145728, cpu_seconds: 12, since: '2026-09-04T18:02:00Z', managed: true}],
    ['chrony', 'system', 'Time synchronisation (chrony)', {unit_file_state: 'enabled', running: true, pid: 702, user: 'chrony', memory_bytes: 2097152, cpu_seconds: 3, since: '2026-09-04T18:01:00Z', managed: true}],
];

async function prepare(page: Page, language: 'de' | 'en') {
    await page.addInitScript((l) => {
        localStorage.setItem('ol.language', l);
        localStorage.setItem('ol.services.hideSystem', '0');
        localStorage.setItem('ol.services.hideOccu', '0');
    }, language);
    await page.route('**/api/system/v1/services', async (route) => {
        if (route.request().method() !== 'GET') return route.fallback();
        const response = await route.fetch();
        const body = (await response.json()) as {services: Record<string, unknown>[]};
        for (const [id, category, description, more] of EXTRA) body.services.push({id, kind: 'system', running: false, enabled: true, managed: false, category, description, ...more});
        // an addon outside its unit: the longest Status cell
        Object.assign(body.services.find((s) => s.id === 'addon-hm2mqtt')!, {stray: true});
        // the interface processes' ports on a real box (internal/system/services.go)
        Object.assign(body.services.find((s) => s.id === 'rfd')!, {port: 32001});
        Object.assign(body.services.find((s) => s.id === 'hmipserver')!, {port: 32010});
        Object.assign(body.services.find((s) => s.id === 'hs485d')!, {port: 32000});
        await route.fulfill({response, json: body});
    });
    await page.goto('/system/services');
    await expect(page.locator('table.ol-table').first().locator('tbody tr')).toHaveCount(9 + EXTRA.length);
}

// Task 80: the table has no Protocol column, and the line under the id (B-75) no protocol or port.
// The API still lists both; the Interfaces page's process cards show the port.
test.describe('the Services table without the protocol', () => {
    test.skip(({isMobile}) => isMobile, 'the columns and the line under the id are a desktop matter; the phone stacks the rows');

    const HEADERS = {
        en: ['Service', 'Kind', 'Description', 'PID', 'Status', 'Enabled', 'User', 'Memory', 'CPU', 'Uptime', ''],
        de: ['Dienst', 'Art', 'Beschreibung', 'PID', 'Status', 'Aktiviert', 'Benutzer', 'Arbeitsspeicher', 'CPU', 'Betriebszeit', ''],
    };

    for (const language of ['en', 'de'] as const) {
        test(`no Protocol in the header, in ${language === 'en' ? 'English' : 'German'}`, async ({page}) => {
            await page.setViewportSize({width: 1600, height: 900});
            await prepare(page, language);
            const headers = (await page.locator('table.ol-table').first().locator('thead th').allTextContents()).map((h) => h.replace(/[▲▼]/g, '').trim());
            expect(headers).toEqual(HEADERS[language]);
            await expect(page.locator('table.ol-table').first()).not.toContainText(':32001');
        });
    }

    test('at 1280 px the line under an interface process is its kind and description', async ({page}) => {
        await page.setViewportSize({width: 1280, height: 900});
        await prepare(page, 'en');
        const rfd = page.locator('table.ol-table').first().locator('tbody tr').filter({has: page.locator('.sv-id', {hasText: /^rfd$/})});
        await expect(rfd.locator('.sv-sub')).toBeVisible();
        await expect(rfd.locator('.sv-sub')).toHaveText('core · BidCos-RF interface process');
    });
});

// how far the page, each table and each row's last action reach, against the window. B-129: the
// page's width is the scroll port's where the port carries the overflow - a scroll container does
// not pass it on to the document (test/e2e/scroll.ts).
async function reach(page: Page) {
    return page.evaluate((s) => {
        const right = (sel: string) => Math.max(0, ...[...document.querySelectorAll(sel)].map((e) => Math.ceil(e.getBoundingClientRect().right)));
        return {
            window: document.documentElement.clientWidth,
            page: Math.max(document.documentElement.scrollWidth, document.querySelector(s)?.scrollWidth ?? 0),
            tables: right('table.ol-table'),
            actions: right('.ol-rowactions > *'),
        };
    }, PORT);
}

function expectFits(r: Awaited<ReturnType<typeof reach>>, what: string) {
    expect(r.page, `${what}: page ${r.page} px in a ${r.window} px window`).toBeLessThanOrEqual(r.window);
    expect(r.tables, `${what}: a table reaches ${r.tables} px`).toBeLessThanOrEqual(r.window);
    expect(r.actions, `${what}: an action reaches ${r.actions} px`).toBeLessThanOrEqual(r.window);
}

test.describe('the Services page fits the window', () => {
    test.skip(({isMobile}) => isMobile, 'a tablet and desktop widths; the phone stacks the rows (phone-width.spec.ts)');

    // the last action of every row - the menu, or the gap where a row has none - ends inside the
    // window (sideways: the rows further down are below the fold of the window, not past its edge)
    async function expectActionsInside(page: Page) {
        const rows = page.locator('table.ol-table').first().locator('.ol-rowactions');
        const n = await rows.count();
        expect(n).toBe(9 + EXTRA.length);
        const window = await page.evaluate(() => document.documentElement.clientWidth);
        for (let i = 0; i < n; i++) {
            const box = (await rows.nth(i).locator(':scope > *').last().boundingBox())!;
            expect(box.x + box.width, `row ${i}`).toBeLessThanOrEqual(window);
        }
    }

    test('on a 768 px tablet, in German', async ({page}) => {
        await page.setViewportSize({width: 768, height: 1024});
        await prepare(page, 'de');
        expectFits(await reach(page), '768 px, German');
        // every action of a managed row is there: Stop, Restart, Log and the menu
        const actions = page.locator('table.ol-table').first().locator('tbody tr').filter({has: page.locator('.sv-id', {hasText: /^rfd$/})}).locator('.ol-rowactions');
        await expect(actions.locator('.ol-acttext')).toHaveText(['Stoppen', 'Neu starten', 'Protokoll']);
        await expect(actions.getByRole('button', {name: 'Weitere Aktionen'})).toBeVisible();
        await expectActionsInside(page);
    });

    test('on a 1280 px desktop in German with every unit shown', async ({page}) => {
        await page.setViewportSize({width: 1280, height: 900});
        await prepare(page, 'de');
        await expect(page.getByRole('heading', {level: 1, name: 'Dienste'})).toBeVisible();
        await expect(page.locator('table.ol-table').first().locator('thead')).toContainText('Arbeitsspeicher');
        expectFits(await reach(page), '1280 px, German');
        await expectActionsInside(page);
    });

    // task 80: without the Protocol column German needs about 1270 px for every column, so the first
    // step folds Kind and Description away up to 1350 px (it was 1400 px): 1400 and 1351 px show
    // every column, 1350 px the line under the id
    test('at 1400 px and on either side of the 1350 px step, in German and in English', async ({page}) => {
        for (const language of ['de', 'en'] as const) {
            await page.setViewportSize({width: 1600, height: 900});
            await prepare(page, language);
            const kind = page.locator('table.ol-table').first().locator('thead th').nth(1);
            for (const [width, folded] of [[1400, false], [1351, false], [1350, true]] as const) {
                await page.setViewportSize({width, height: 900});
                if (folded) await expect(kind).toBeHidden();
                else await expect(kind).toBeVisible();
                expectFits(await reach(page), `${width} px, ${language}`);
                await expectActionsInside(page);
            }
        }
    });

    test('at every width from 768 to 1600 px, in German and in English', async ({page}) => {
        for (const language of ['de', 'en'] as const) {
            await page.setViewportSize({width: 1600, height: 900});
            await prepare(page, language);
            for (let width = 768; width <= 1600; width += 32) {
                await page.setViewportSize({width, height: 900});
                expectFits(await reach(page), `${width} px, ${language}`);
            }
        }
    });
});
