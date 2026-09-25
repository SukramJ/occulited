import {expect, test, type Locator, type Page, type TestInfo} from '@playwright/test';
import {PORT, scrollY} from './scroll';

// Task 50: timers on the Services page - the table with its Enabled column and row actions, an own
// timer made from a preset and from a custom OnCalendar= expression with the calendar check, edited,
// run, switched off and on, deleted; a shipped timer's Edit… is its override; every unit editor is a
// modal. The stub keeps own timers in memory for all three projects at once, so every test makes its
// timer under a name of its own.

// B-108: a tap on the phone, a click elsewhere. The clicks here were forced while the Services page
// was wider than a phone (B-105): the phone emulation zoomed it out, and a tap missed what it aimed at.
async function press(target: Locator) {
    if (test.info().project.use.isMobile) await target.tap();
    else await target.click();
}

function uniqueName(info: TestInfo) {
    return `e2e${info.project.name.replace(/[^a-z]/g, '').slice(0, 6)}${Date.now().toString(36)}${Math.floor(Math.random() * 1296).toString(36)}`;
}
function timerRow(page: Page, unit: string) {
    return page.locator('table.ol-timers tbody tr').filter({has: page.locator('td:first-child', {hasText: unit})});
}
// an editor is a modal dialog named by its title ("New timer", "Edit timer local-….timer",
// "Edit unit rfd.service")
function editor(page: Page, title: string) {
    return page.getByRole('dialog', {name: title});
}
// the dialog's footer buttons; "Close" alone would also be the × in its title bar
function footer(dialog: Locator, name: string) {
    return dialog.locator('.ol-modal-foot').getByRole('button', {name, exact: true});
}
async function menu(page: Page, unit: string, item: string) {
    const row = timerRow(page, unit);
    await press(row.getByRole('button', {name: 'More actions'}));
    await press(row.getByRole('menuitem', {name: item, exact: true}));
}
// the files as the form writes them, so an opened timer's files follow the form
const timerFile = (name: string, when: string) => `[Unit]\nDescription=${name}\n\n[Timer]\n${when}\n\n[Install]\nWantedBy=timers.target\n`;
const serviceFile = (name: string, command: string) => `[Unit]\nDescription=${name}\n\n[Service]\nType=oneshot\nExecStart=${command}\n`;

test('a new timer from a preset: the files follow the form, the next runs show, it is listed enabled', async ({page}, info) => {
    const name = uniqueName(info);
    const unit = `local-${name}.timer`;
    await page.goto('/system/services');
    await press(page.getByRole('button', {name: 'New timer…'}));
    const ed = editor(page, 'New timer');
    await expect(ed).toBeVisible();
    // the acceptance example: daily at 03:00 running logger hello
    await ed.getByRole('textbox', {name: 'Name', exact: true}).fill(name);
    await expect(ed.getByLabel('When')).toHaveValue('daily');
    await ed.getByLabel('Time', {exact: true}).fill('03:00');
    await ed.getByLabel('Command (ExecStart=)').fill('logger hello');
    await expect(ed.getByRole('textbox', {name: unit})).toHaveValue(timerFile(name, 'OnCalendar=*-*-* 03:00:00\nPersistent=true'));
    await expect(ed.getByRole('textbox', {name: `local-${name}.service`})).toHaveValue(serviceFile(name, 'logger hello'));
    await expect(ed.locator('.te-next li')).toHaveCount(3);

    const post = page.waitForRequest((r) => r.method() === 'POST' && r.url().endsWith('/api/system/v1/timers/own'));
    await press(footer(ed, 'Save'));
    expect((await post).postDataJSON()).toMatchObject({name, timer: timerFile(name, 'OnCalendar=*-*-* 03:00:00\nPersistent=true')});
    await expect(page.locator('pre.ol-notice')).toContainText(`${unit} created`);
    const row = timerRow(page, unit);
    await expect(row).toHaveCount(1);
    await expect(row.locator('.ol-badge')).toHaveText('own');
    await expect(row.locator('.ol-enabled')).toHaveText('Yes');
    // the dialog stays open with the stored timer: nothing unsaved, the name fixed
    const again = editor(page, `Edit timer ${unit}`);
    await expect(again.getByRole('status')).toHaveText('Saved.');
    await expect(again.getByRole('textbox', {name: 'Name', exact: true})).toBeDisabled();
    await expect(footer(again, 'Save')).toBeDisabled();
    await press(footer(again, 'Close'));
    await expect(again).toBeHidden();

    // the same name again is refused with the box's reason, and nothing new is listed
    await press(page.getByRole('button', {name: 'New timer…'}));
    const second = editor(page, 'New timer');
    await second.getByRole('textbox', {name: 'Name', exact: true}).fill(name);
    await second.getByLabel('Command (ExecStart=)').fill('logger again');
    await press(footer(second, 'Save'));
    await expect(second.getByRole('alert')).toContainText('exists already');
    await expect(timerRow(page, unit)).toHaveCount(1);
});

test('a new timer with a custom expression: a typo shows before saving, a hand-edited file keeps its edit', async ({page}, info) => {
    const name = uniqueName(info);
    await page.goto('/system/services');
    await press(page.getByRole('button', {name: 'New timer…'}));
    const ed = editor(page, 'New timer');
    await ed.getByRole('textbox', {name: 'Name', exact: true}).fill(name);
    await ed.getByLabel('When').selectOption('custom');
    const expression = ed.getByRole('textbox', {name: 'OnCalendar='});
    await expression.fill('bogus');
    await expect(ed.locator('.te-check .ol-warn')).toContainText('Failed to parse');
    await expression.fill('Mon..Fri *-*-* 07:30');
    await expect(ed.locator('.te-next li')).toHaveCount(3);
    const file = ed.getByRole('textbox', {name: `local-${name}.timer`});
    await expect(file).toHaveValue(/\nOnCalendar=Mon\.\.Fri \*-\*-\* 07:30\nPersistent=true\n/);

    // a directive the form does not have, written into the file: the form no longer changes it
    const edited = (await file.inputValue()).replace('\n\n[Install]', '\nAccuracySec=1min\n\n[Install]');
    await file.fill(edited);
    await expect(ed.getByText('edited by hand')).toHaveCount(1);
    // task 51: the state opens what it means; Escape closes the popup and not the dialog
    await press(ed.getByRole('button', {name: 'edited by hand'}));
    await expect(page.getByRole('tooltip')).toHaveText('The form no longer changes this file.');
    await page.keyboard.press('Escape');
    await expect(page.getByRole('tooltip')).toHaveCount(0);
    await expect(ed).toBeVisible();
    // the shell note of the command and the editor's own help are behind their ?s
    await expect(ed.getByText('Runs without a shell')).toHaveCount(0);
    await press(ed.locator('label', {hasText: 'Command (ExecStart=)'}).getByRole('button', {name: 'Help'}));
    await expect(page.getByRole('tooltip')).toContainText("/bin/sh -c '…'");
    await press(ed.getByRole('heading').getByRole('button', {name: 'Help'}));
    await expect(page.getByRole('tooltip')).toHaveText('What is saved are these two files; any directive the form does not have can be written into them.');
    await page.keyboard.press('Escape');
    await expect(ed).toBeVisible();
    await ed.getByLabel('Command (ExecStart=)').fill('/bin/sh -c "date >> /tmp/stamp"');
    await expect(file).toHaveValue(edited);
    await expect(ed.getByRole('textbox', {name: `local-${name}.service`})).toHaveValue(/ExecStart=\/bin\/sh -c "date >> \/tmp\/stamp"\n/);

    const post = page.waitForRequest((r) => r.method() === 'POST' && r.url().endsWith('/api/system/v1/timers/own'));
    await press(footer(ed, 'Save'));
    expect((await post).postDataJSON().timer).toContain('AccuracySec=1min');
    await expect(timerRow(page, `local-${name}.timer`)).toHaveCount(1);
});

test('an own timer: Edit… changes its schedule, Run now, Disable and Enable, Delete', async ({page}, info) => {
    const name = uniqueName(info);
    const unit = `local-${name}.timer`;
    const made = await page.request.post('/api/system/v1/timers/own', {data: {name, timer: timerFile(name, 'OnCalendar=hourly'), service: serviceFile(name, 'logger hi')}});
    expect(made.status()).toBe(201);
    await page.goto('/system/services');
    const row = timerRow(page, unit);
    await expect(row).toHaveCount(1);

    await menu(page, unit, 'Edit…');
    const ed = editor(page, `Edit timer ${unit}`);
    await expect(ed.getByLabel('When')).toHaveValue('hourly');
    await expect(ed.getByLabel('Catch up after the system was off (Persistent=)')).not.toBeChecked();
    await ed.getByLabel('When').selectOption('daily');
    await ed.getByLabel('Time', {exact: true}).fill('04:30');
    await expect(ed.getByRole('textbox', {name: unit})).toHaveValue(/\nOnCalendar=\*-\*-\* 04:30:00\n/);
    const put = page.waitForRequest((r) => r.method() === 'PUT' && r.url().includes(`/api/system/v1/timers/own/${name}`));
    await press(footer(ed, 'Save'));
    expect((await put).postDataJSON().timer).toContain('OnCalendar=*-*-* 04:30:00');
    await expect(ed.getByRole('status')).toHaveText('Saved.');
    await press(footer(ed, 'Close'));
    await expect(ed).toBeHidden();

    const run = page.waitForRequest((r) => r.method() === 'POST' && r.url().endsWith(`/api/system/v1/timers/own/${unit}/run`));
    await press(row.locator('.ol-rowactions button:has(.ol-acttext:text-is("Run now"))'));
    await run;
    await expect(page.locator('pre.ol-notice')).toContainText(`local-${name}.service started`);
    await expect(row.getByRole('link', {name: 'Log'})).toHaveAttribute('href', `/system/log?unit=local-${name}`);

    await menu(page, unit, 'Disable');
    await expect(page.getByRole('dialog')).toContainText(`Disable ${unit}?`);
    await press(page.getByRole('dialog').getByRole('button', {name: 'OK'}));
    await expect(row.locator('.ol-enabled')).toHaveText('No');
    await menu(page, unit, 'Enable');
    await expect(row.locator('.ol-enabled')).toHaveText('Yes');

    await menu(page, unit, 'Delete');
    const dialog = page.getByRole('dialog');
    await expect(dialog).toContainText(`Delete ${unit}?`);
    await press(dialog.getByRole('button', {name: 'Delete'}));
    await expect(row).toHaveCount(0);
    await expect(page.locator('pre.ol-notice')).toContainText(`${unit} deleted.`);
});

test('a shipped timer: Run now asks first, Edit… opens its override, no Delete, an unsaved close asks', async ({page}) => {
    await page.route('**/api/system/v1/services/occulite-backup.timer/unit', (route) =>
        route.fulfill({json: {unit: 'occulite-backup.timer', effective: '# /usr/lib/systemd/system/occulite-backup.timer\n[Timer]\nOnCalendar=*-*-* 00:07:00\n', override: '', path: '/run/systemd/system/occulite-backup.timer.d/50-occulite.conf'}}),
    );
    const started: string[] = [];
    await page.route('**/api/system/v1/services/*/start', (route) => {
        started.push(new URL(route.request().url()).pathname);
        return route.fulfill({json: {ok: true, output: ''}});
    });
    await page.goto('/system/services');
    const unit = 'occulite-backup.timer';
    const row = timerRow(page, unit);
    await expect(row.locator('.ol-enabled')).toHaveText('Yes');
    await expect(row.locator('.ol-badge')).toHaveCount(0);

    await press(row.locator('.ol-rowactions button:has(.ol-acttext:text-is("Run now"))'));
    await expect(page.getByRole('dialog')).toContainText('Run occulite-backup.service now?');
    await press(page.getByRole('dialog').getByRole('button', {name: 'OK'}));
    await expect.poll(() => started).toEqual(['/api/system/v1/services/occulite-backup.service/start']);

    await press(row.getByRole('button', {name: 'More actions'}));
    await expect(row.getByRole('menuitem', {name: 'Delete'})).toHaveCount(0);
    await press(row.getByRole('menuitem', {name: 'Edit…'}));
    const ed = editor(page, `Edit unit ${unit}`);
    await expect(ed).toContainText('OnCalendar=*-*-* 00:07:00');
    await ed.getByRole('textbox', {name: 'Override'}).fill('[Timer]\nRandomizedDelaySec=10min\n');
    await press(footer(ed, 'Close'));
    const question = page.getByRole('dialog', {name: 'Discard changes?'});
    await expect(question).toBeVisible();
    await press(question.getByRole('button', {name: 'Cancel'}));
    await expect(question).toBeHidden();
    await expect(ed).toBeVisible();
    await press(footer(ed, 'Close'));
    await press(question.getByRole('button', {name: 'Discard'}));
    await expect(ed).toBeHidden();
});

// B-68: a timer's runs in the reader's language and time zone, not RFC 3339; the countdown the box
// sends (Go's `2h3m1s`) in the page's own words; the whole stamp as the title; and on a phone a
// cell is two lines at most, where it was four
test('the Timers table shows its runs localized, in English and in German', async ({page}) => {
    const next = new Date(Date.now() + (2 * 3600 + 3 * 60 + 1) * 1000).toISOString();
    const last = new Date(Date.now() - 3600 * 1000).toISOString();
    await page.route('**/api/system/v1/timers', async (route) => {
        if (route.request().method() !== 'GET') return route.fallback();
        const response = await route.fetch();
        const body = (await response.json()) as {timers: Record<string, unknown>[]};
        Object.assign(body.timers.find((x) => x.unit === 'occulite-backup.timer')!, {next, last, left: '2h3m1s'});
        await route.fulfill({response, json: body});
    });
    // what the page must show, computed in the browser's own time zone
    const short = (iso: string, locale: string) =>
        page.evaluate(([iso, locale]) => {
            const d = new Date(iso!);
            const year = d.getFullYear() !== new Date().getFullYear() ? {year: 'numeric' as const} : {};
            return d.toLocaleString(locale, {day: '2-digit', month: '2-digit', hour: '2-digit', minute: '2-digit', hourCycle: 'h23', ...year});
        }, [iso, locale]);
    const lines = (cell: Locator) =>
        cell.evaluate((e) => {
            const range = document.createRange();
            range.selectNodeContents(e);
            const tops = [...range.getClientRects()].map((r) => r.top).sort((a, b) => a - b);
            return tops.filter((top, i) => i === 0 || top - tops[i - 1]! > 4).length;
        });
    for (const [language, locale, nextHeader, lastHeader] of [['en', 'en-GB', 'Next', 'Last'], ['de', 'de-DE', 'Nächster Lauf', 'Letzter Lauf']] as const) {
        await page.goto('/system/services');
        await page.evaluate((l) => localStorage.setItem('ol.language', l), language);
        await page.reload();
        const table = page.locator('table.ol-timers');
        await expect(table.locator('thead th', {hasText: nextHeader})).toHaveCount(1);
        const headers = (await table.locator('thead th').allTextContents()).map((h) => h.trim());
        const row = timerRow(page, 'occulite-backup.timer');
        const nextCell = row.locator('td').nth(headers.indexOf(nextHeader));
        const lastCell = row.locator('td').nth(headers.indexOf(lastHeader));
        await expect(nextCell).toHaveText(`${await short(next, locale)} (in 2 h 3 min)`);
        await expect(lastCell).toHaveText(await short(last, locale));
        expect(await row.textContent()).not.toMatch(/\d{4}-\d{2}-\d{2}T/);
        // the whole stamp, to the second, where the cell is short
        await expect(nextCell).toHaveAttribute('title', /\d{2}:\d{2}:\d{2}/);
        await expect(lastCell).toHaveAttribute('title', /\d{2}:\d{2}:\d{2}/);
        expect(await lines(nextCell)).toBeLessThanOrEqual(2);
        expect(await lines(lastCell)).toBe(1);
    }
});

test('Edit unit… on a service: Save keeps the dialog open with the effective text reloaded and a notice', async ({page}) => {
    let saved = '';
    await page.route('**/api/system/v1/services/rfd/unit', async (route) => {
        if (route.request().method() !== 'PUT') return route.fallback();
        saved = (route.request().postDataJSON() as {override: string}).override;
        return route.fulfill({json: {unit: 'rfd.service', effective: `# /usr/lib/systemd/system/rfd.service\n[Service]\nExecStart=/bin/rfd -f /etc/rfd.conf\n\n# /run/systemd/system/rfd.service.d/50-occulite.conf\n${saved}`, override: saved, path: '/run/systemd/system/rfd.service.d/50-occulite.conf'}});
    });
    await page.goto('/system/services');
    const rfd = page.locator('table.ol-table').first().locator('tbody tr').filter({has: page.locator('td:first-child .sv-id', {hasText: /^rfd$/})});
    await press(rfd.getByRole('button', {name: 'More actions'}));
    await press(rfd.getByRole('menuitem', {name: 'Edit unit…'}));
    const ed = editor(page, 'Edit unit rfd.service');
    await expect(ed).toBeVisible();
    // task 51: what an override is, behind the ? of the dialog's title
    await expect(ed.getByText('what you edit is an override')).toHaveCount(0);
    await press(ed.getByRole('heading').getByRole('button', {name: 'Help'}));
    await expect(page.getByRole('tooltip')).toContainText('what you edit is an override');
    await page.keyboard.press('Escape');
    await expect(page.getByRole('tooltip')).toHaveCount(0);
    await expect(ed).toBeVisible();
    const text = ed.getByRole('textbox', {name: 'Override'});
    await text.fill('[Service]\nEnvironment=RFD_DEBUG=2\n');
    await press(footer(ed, 'Save override'));
    await expect(ed.getByRole('status')).toContainText('Override saved and systemd reloaded');
    await expect(ed.locator('pre.effective')).toContainText('RFD_DEBUG=2');
    expect(saved).toBe('[Service]\nEnvironment=RFD_DEBUG=2\n');
    // saved, so nothing to lose: Close closes without a question
    await press(footer(ed, 'Close'));
    await expect(ed).toBeHidden();
    await expect(page.getByRole('dialog')).toHaveCount(0);
});

test('the editors are modal: focus inside, two columns or stacked, Escape and × close, unsaved changes ask, the page stays put', async ({page}, info) => {
    await page.goto('/system/services');
    await expect(page.locator('table.ol-timers')).toBeVisible();
    await page.getByRole('button', {name: 'New timer…'}).scrollIntoViewIfNeeded();
    const scrolled = await scrollY(page);
    await press(page.getByRole('button', {name: 'New timer…'}));
    const ed = editor(page, 'New timer');
    await expect(ed).toBeVisible();
    const name = ed.getByRole('textbox', {name: 'Name', exact: true});
    await expect(name).toBeFocused();

    // the form and its files: side by side on a desktop, one above the other on a phone
    const when = (await ed.getByLabel('When').boundingBox())!;
    const file = (await ed.locator('textarea').first().boundingBox())!;
    if (info.project.name === 'phone') expect(file.y).toBeGreaterThan(when.y + when.height);
    else expect(file.x).toBeGreaterThan(when.x + when.width);
    // the dialog fits the viewport; the page behind does not scroll under it
    const box = (await ed.boundingBox())!;
    const viewport = page.viewportSize()!;
    expect(box.width).toBeLessThanOrEqual(viewport.width);
    expect(box.height).toBeLessThanOrEqual(viewport.height * 0.9 + 1);
    // B-129: the lock is on the shell's scrolling box, not on the root
    expect(await page.evaluate((sel) => getComputedStyle(document.querySelector(sel)!).overflow, PORT)).toBe('hidden');
    await page.mouse.move(2, 2);
    await page.mouse.wheel(0, 800);
    await page.waitForTimeout(200);
    expect(await scrollY(page)).toBe(scrolled);

    // Tab stays inside the dialog
    for (let i = 0; i < 25; i++) await page.keyboard.press('Tab');
    expect(await ed.evaluate((el) => el.contains(document.activeElement))).toBe(true);

    // an untouched editor closes on Escape without a question
    await page.keyboard.press('Escape');
    await expect(ed).toBeHidden();
    expect(await page.evaluate((sel) => getComputedStyle(document.querySelector(sel)!).overflow, PORT)).not.toBe('hidden');

    // with a change, × and Escape ask; Cancel keeps the dialog, Discard closes it
    await press(page.getByRole('button', {name: 'New timer…'}));
    await editor(page, 'New timer').getByRole('textbox', {name: 'Name', exact: true}).fill('unsaved');
    await press(ed.getByRole('button', {name: 'Close'}).first());
    const question = page.getByRole('dialog', {name: 'Discard changes?'});
    await expect(question).toBeVisible();
    const overflow = () => page.evaluate((sel) => getComputedStyle(document.querySelector(sel)!).overflow, PORT);
    // task 51: the lock is ModalDialog's and counted - the question over the editor is a second
    // holder, and closing it leaves the page locked while the editor is still open
    expect(await overflow()).toBe('hidden');
    await press(question.getByRole('button', {name: 'Cancel'}));
    await expect(question).toBeHidden();
    await expect(ed).toBeVisible();
    expect(await overflow()).toBe('hidden');
    await ed.getByRole('textbox', {name: 'Name', exact: true}).focus();
    await page.keyboard.press('Escape');
    await expect(question).toBeVisible();
    await press(question.getByRole('button', {name: 'Discard'}));
    await expect(ed).toBeHidden();
    await expect(timerRow(page, 'local-unsaved.timer')).toHaveCount(0);
    await expect.poll(overflow).not.toBe('hidden');
});
