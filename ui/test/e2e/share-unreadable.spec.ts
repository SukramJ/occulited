import {type Page, type Route} from '@playwright/test';
import {expect, test} from './fixtures';

// openccu-lite B-223: on an NFS export that maps root to nobody (root_squash, TrueNAS' default) root
// can write and the system's own user cannot read back what root wrote. The share's Test says so
// with the hint, the Journal panel says the copies are written but cannot be shown, and the Log
// page says its lines are RAM's alone - instead of silently showing only RAM.

const HINT = 'On an NFS export that maps root to nobody (root_squash), map root to root (TrueNAS: Maproot User root) or map all users to one account (TrueNAS: Mapall User).';
const HINT_DE = 'Bei einem NFS-Export, der root auf nobody abbildet (root_squash), root auf root abbilden (TrueNAS: Maproot User root) oder alle Benutzer auf ein Konto (TrueNAS: Mapall User).';

async function unreadableTest(page: Page) {
    await page.route('**/api/system/v1/storage/shares/media/test', (route: Route) =>
        route.fulfill({json: {ok: true, state: 'unreadable', step: 'read-back', error: "the system's own user cannot read what root wrote here: /media/net/media: permission denied", fs_type: 'nfs', free_bytes: 1e12, total_bytes: 4e12, write_mbps: 40}}),
    );
}

test("the share's Test: written, not readable by the system, and the hint", async ({page, baseURL}) => {
    await page.context().addCookies([{name: 'stub-shares', value: `s-${Math.random().toString(36).slice(2)}`, url: baseURL!}]);
    await unreadableTest(page);
    await page.goto('/system/storage');
    const media = page.locator('[data-share="media"]');
    await media.getByRole('button', {name: 'Test'}).click();
    const r = media.locator('[data-test-result="unreadable"]');
    await expect(r).toHaveText(`Writable, but the system cannot read back what it wrote: the Log page cannot show the journal's copies there. ${HINT}`);
    await expect(r).not.toHaveClass(/\berror\b/);
});

test('the Test in German', async ({page, baseURL}) => {
    await page.addInitScript(() => localStorage.setItem('ol.language', 'de'));
    await page.context().addCookies([{name: 'stub-shares', value: `s-${Math.random().toString(36).slice(2)}`, url: baseURL!}]);
    await unreadableTest(page);
    await page.goto('/system/storage');
    const media = page.locator('[data-share="media"]');
    await media.getByRole('button', {name: 'Testen'}).click();
    await expect(media.locator('[data-test-result="unreadable"]')).toHaveText(`Beschreibbar, aber das System kann nicht zurücklesen, was es geschrieben hat: Die Protokollseite kann die Kopien des Journals dort nicht anzeigen. ${HINT_DE}`);
});

const SHARE_ON = {storage: 'ram-sync', persist: '0', platform: 'rpi4', default_storage: 'ram', default_persistent: false, persistent: false, effective: 'ram-sync', target: 'share:nas/ccu/journal', target_share: 'nas', target_dir: 'ccu/journal', target_path: '/media/net/nas/ccu/journal', target_usage: null, target_free: null, target_ok: true, last_sync: new Date().toISOString(), last_sync_result: 'ok', last_sync_copied: 3, usage: ''};

async function journal(page: Page, patch: Record<string, unknown>) {
    await page.route('**/api/system/v1/journal', async (route: Route) => {
        if (route.request().method() !== 'GET') return route.fallback();
        const response = await route.fetch();
        await route.fulfill({response, json: {...(await response.json()), ...SHARE_ON, ...patch}});
    });
}

test('the Journal panel: the copies are written and cannot be shown', async ({page}) => {
    await journal(page, {target_unreadable: true});
    await page.goto('/system/log?settings=journal');
    const panel = page.locator('.jr-panel');
    await expect(panel.locator('[data-journal-unreadable]')).toHaveText(`The copies are written to the network share nas, but the system cannot read them back, so the Log page cannot show them. ${HINT}`);
});

test('the Journal panel: readable copies, no warning', async ({page}) => {
    await journal(page, {});
    await page.goto('/system/log?settings=journal');
    await expect(page.locator('#ol-jr-storage')).toBeVisible();
    await expect(page.locator('[data-journal-unreadable]')).toHaveCount(0);
});

async function logWithUnreadable(page: Page) {
    await page.route('**/api/system/v1/log?*', async (route: Route) => {
        const response = await route.fetch();
        await route.fulfill({response, json: {...(await response.json()), copies_unreadable: {path: '/media/net/nas/ccu/journal', error: 'permission denied', hint: 'x'}}});
    });
}

test('the Log page: its lines are RAM only, and why', async ({page}) => {
    await logWithUnreadable(page);
    await page.goto('/system/log');
    const n = page.locator('[data-notice="copies-unreadable"]');
    await expect(n).toContainText(`The journal's copies in /media/net/nas/ccu/journal cannot be read by the system: the lines shown are from RAM only. ${HINT}`);
    await expect(page.locator('.ol-line').first()).toBeVisible();
    await n.getByRole('button', {name: 'Storage settings'}).click();
    await expect(page.getByRole('dialog', {name: 'Log settings'}).getByRole('tab', {name: 'Journal'})).toHaveAttribute('aria-selected', 'true');
});

test('the Log page without such copies says nothing', async ({page}) => {
    await page.goto('/system/log');
    await expect(page.locator('.ol-line').first()).toBeVisible();
    await expect(page.locator('[data-notice="copies-unreadable"]')).toHaveCount(0);
});

test('the Log page in German', async ({page}) => {
    await page.addInitScript(() => localStorage.setItem('ol.language', 'de'));
    await logWithUnreadable(page);
    await page.goto('/system/log');
    await expect(page.locator('[data-notice="copies-unreadable"]')).toContainText(`Die Kopien des Journals in /media/net/nas/ccu/journal kann das System nicht lesen: Die angezeigten Zeilen stammen nur aus dem RAM. ${HINT_DE}`);
});
