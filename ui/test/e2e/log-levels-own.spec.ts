import {test, expect, type Page} from '@playwright/test';

// Task 101: the Log settings' levels tab has occulited's own level at the top (with debug for some
// areas only) and multimacd's own level beside rfd's, which says before saving that the radio stack
// restarts. Task 297: multimacd offers only Debug and Info and no longer follows rfd. The PUT is
// answered here as the box answers it.

type Put = {multimacd: number; rfd: number; occulited: {level: string; debug_areas: string[]}};

async function openLevels(page: Page, puts: Put[], answer: (body: Put) => {restart: string[]; applied: string[]}) {
    await page.route('**/api/system/v1/loglevels', async (route) => {
        if (route.request().method() !== 'PUT') return route.fallback();
        const body = route.request().postDataJSON() as Put;
        puts.push(body);
        await route.fulfill({json: {...body, ...answer(body)}});
    });
    await page.goto('/system/log');
    await page.getByRole('button', {name: 'Log settings'}).click();
    await expect(page.locator('#ol-lv-occulited')).toBeVisible();
}

test('occulited is the first row, multimacd offers Debug and Info of its own, and a change restarts the radio stack', async ({page}) => {
    const puts: Put[] = [];
    const restarts: string[] = [];
    await page.route('**/api/system/v1/radio/restart', (route) => {
        restarts.push(route.request().method());
        return route.fulfill({json: {stopped: ['hmipserver', 'rfd', 'multimacd'], started: ['multimacd', 'rfd', 'hmipserver']}});
    });
    await openLevels(page, puts, (b) => ({restart: b.multimacd === 1 ? ['multimacd'] : [], applied: []}));

    // occulited's row comes before every daemon's
    await expect(page.locator('.lv-grid [data-level], .lv-grid > label.lv-field').first()).toHaveAttribute('data-level', 'occulited');
    const mm = page.locator('#ol-lv-multimacd');
    // two choices, Info by default; no "same as rfd", no quieter level
    await expect(mm.locator('option')).toHaveText(['Debug (1)', 'Info (2)']);
    await expect(mm.locator('option:checked')).toHaveText('Info (2)');
    const note = page.locator('[data-note="radio-restart"]');
    await expect(note).toBeHidden();
    // the rfd row no longer speaks of multimacd
    await expect(page.locator('[data-level="rfd"]')).not.toContainText('multimacd');

    // rfd's level does not move multimacd's, and asks for no restart of the radio stack
    await page.locator('#ol-lv-rfd').selectOption('1');
    await expect(mm.locator('option:checked')).toHaveText('Info (2)');
    await expect(note).toBeHidden();
    await page.locator('#ol-lv-rfd').selectOption('5');

    // Debug: the note says so before saving
    await mm.selectOption({label: 'Debug (1)'});
    await expect(note).toBeVisible();
    await expect(note).toContainText('hmipserver, rfd and multimacd stop and start again');
    await page.getByRole('button', {name: 'Save', exact: true}).click();
    await expect.poll(() => puts.length).toBe(1);
    expect(puts[0]!.multimacd).toBe(1);
    expect(puts[0]!.rfd).toBe(5);
    expect(puts[0]!.occulited).toEqual({level: 'info', debug_areas: []});
    // saved: the note is gone, the restart is offered as the radio stack's
    await expect(note).toBeHidden();
    const restart = page.getByRole('button', {name: 'Restart the radio stack'});
    await expect(restart).toBeVisible();
    await expect(page.getByRole('button', {name: 'Restart multimacd'})).toHaveCount(0);
    await restart.click();
    await expect(restart).toBeHidden();
    expect(restarts).toEqual(['POST']);
    await expect(page.getByText('The radio stack restarted: hmipserver, rfd and multimacd stopped and started again.')).toBeVisible();

    // back to Info: 2 in the PUT, never null
    await mm.selectOption({label: 'Info (2)'});
    await expect(note).toBeVisible();
    await page.getByRole('button', {name: 'Save', exact: true}).click();
    await expect.poll(() => puts.length).toBe(2);
    expect(puts[1]!.multimacd).toBe(2);
});

// Task 297: an older daemon that still answers null or a stored quieter level shows Info, as the
// system runs it; a refused level (422) is said on the page, and nothing is taken as saved.
test("multimacd's stale level shows as Info, and a refusal is shown", async ({page}) => {
    for (const stale of [null, 5]) {
        await page.unroute('**/api/system/v1/loglevels');
        await page.route('**/api/system/v1/loglevels', async (route) => {
            if (route.request().method() === 'PUT') {
                return route.fulfill({status: 422, json: {error: 'invalid', message: "multimacd's level is 1 (debug) or 2 (info)"}});
            }
            const r = await route.fetch();
            await route.fulfill({json: {...(await r.json()), multimacd: stale}});
        });
        await page.goto('/system/log');
        await page.getByRole('button', {name: 'Log settings'}).click();
        const mm = page.locator('#ol-lv-multimacd');
        await expect(mm.locator('option:checked')).toHaveText('Info (2)');
        await expect(mm.locator('option')).toHaveCount(2);
        await expect(page.locator('[data-note="radio-restart"]')).toBeHidden();
    }
    await page.locator('#ol-lv-multimacd').selectOption({label: 'Debug (1)'});
    await page.getByRole('button', {name: 'Save', exact: true}).click();
    await expect(page.getByText("multimacd's level is 1 (debug) or 2 (info)")).toBeVisible();
    await expect(page.locator('[data-note="radio-restart"]')).toBeVisible();
});

test("occulited's level and its debug areas apply live, with the note on debug", async ({page}) => {
    const puts: Put[] = [];
    await openLevels(page, puts, () => ({restart: [], applied: ['occulited']}));
    const level = page.locator('#ol-lv-occulited');
    await expect(level.locator('option:checked')).toHaveText('Info');
    const debugNote = page.locator('[data-note="debug"]');
    await expect(debugNote).toBeHidden();

    // one area at debug: the note appears
    await page.getByRole('checkbox', {name: 'HTTP requests'}).check();
    await page.getByRole('checkbox', {name: 'Certificate (ACME)'}).check();
    await expect(debugNote).toBeVisible();
    await page.getByRole('button', {name: 'Save', exact: true}).click();
    await expect.poll(() => puts.length).toBe(1);
    expect(puts[0]!.occulited).toEqual({level: 'info', debug_areas: ['acme', 'http']});
    await expect(page.getByText('occulited took the level live.')).toBeVisible();
    // nothing to restart for occulited
    await expect(page.locator('.ol-actions button', {hasText: /Restart/})).toHaveCount(0);

    // at debug the areas say nothing more and are hidden; warn with no area: the note goes
    await level.selectOption('debug');
    await expect(page.getByRole('checkbox', {name: 'HTTP requests'})).toHaveCount(0);
    await expect(debugNote).toBeVisible();
    await level.selectOption('warn');
    await page.getByRole('checkbox', {name: 'HTTP requests'}).uncheck();
    await page.getByRole('checkbox', {name: 'Certificate (ACME)'}).uncheck();
    await expect(debugNote).toBeHidden();
    await page.getByRole('button', {name: 'Save', exact: true}).click();
    await expect.poll(() => puts.length).toBe(2);
    expect(puts[1]!.occulited).toEqual({level: 'warn', debug_areas: []});
});

// B-162: the numbers are eQ-3's own (1 the most, 7 the least), the page offers four of them, and
// a file that carries another number shows it without letting it be chosen again.
test('debug says what it costs, and a number the page does not offer is shown as stored', async ({page}) => {
    await page.route('**/api/system/v1/loglevels', async (route) => {
        if (route.request().method() !== 'GET') return route.fallback();
        const r = await route.fetch();
        await route.fulfill({json: {...(await r.json()), hs485d: 7}});
    });
    await page.goto('/system/log');
    await page.getByRole('button', {name: 'Log settings'}).click();

    // hs485d carries 7, which the page does not offer
    const hs = page.locator('#ol-lv-hs485d');
    await expect(hs.locator('option:checked')).toHaveText('7 · stored, not one of the levels');
    await expect(hs.locator('option:checked')).toBeDisabled();
    await expect(page.locator('[data-note="hs485d-stale"]')).toContainText('1 being the most and 7 the least');
    await hs.selectOption('4');
    await expect(page.locator('[data-note="hs485d-stale"]')).toHaveCount(0);
    await expect(hs.locator('option', {hasText: 'stored'})).toHaveCount(0);

    // rfd at 1 is debug: the row says what that costs
    const rfd = page.locator('#ol-lv-rfd');
    await expect(page.locator('[data-note="rfd-debug"]')).toHaveCount(0);
    await rfd.selectOption('1');
    await expect(page.locator('[data-note="rfd-debug"]')).toContainText('writes every step');
    await rfd.selectOption('5');
    await expect(page.locator('[data-note="rfd-debug"]')).toHaveCount(0);
});

// German, and the sheet at a phone's and a tablet's width: nothing reaches past the window
test('the two rows in German fit a phone and a 768 px window', async ({page}) => {
    await page.addInitScript(() => localStorage.setItem('ol.language', 'de'));
    await page.goto('/system/log');
    await page.getByRole('button', {name: 'Protokoll-Einstellungen'}).click();
    const mm = page.locator('#ol-lv-multimacd');
    await expect(mm.locator('option:checked')).toHaveText('Info (2)');
    await expect(mm.locator('option')).toHaveText(['Debug (1)', 'Info (2)']);
    await expect(page.getByText('Debug nur für diese Bereiche')).toBeVisible();
    await mm.selectOption({label: 'Debug (1)'});
    await expect(page.locator('[data-note="radio-restart"]')).toContainText('Neustart des Funk-Stacks');
    await page.getByRole('checkbox', {name: 'Anmeldungen'}).check();
    await expect(page.locator('[data-note="debug"]')).toContainText('Debug schreibt viele Zeilen');
    for (const width of [412, 768]) {
        await page.setViewportSize({width, height: 900});
        const o = await page.evaluate(() => {
            const vw = document.documentElement.clientWidth;
            const wide = Array.from(document.querySelectorAll('[role="dialog"] *'))
                .map((el) => ({el, r: el.getBoundingClientRect()}))
                .filter(({r}) => r.width > 0 && r.right > vw + 1)
                .map(({el, r}) => `${el.tagName.toLowerCase()}.${el.getAttribute('class') ?? ''} to ${Math.round(r.right)}`);
            return {page: document.documentElement.scrollWidth, vw, wide: wide.slice(0, 5)};
        });
        expect(o.page, `${width}: ${o.wide.join('; ')}`).toBeLessThanOrEqual(o.vw);
        expect(o.wide, `${width} px`).toEqual([]);
    }
});
