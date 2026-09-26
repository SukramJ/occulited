import {expect, test, type Page} from '@playwright/test';
import {fitsWindow} from './scroll';

// B-21 (maintainer, 2026-09-26): a catalogue install that cannot read the addon's release list
// refuses and installs nothing, and the Addons page says why - for GitHub's rate limit with the
// wait from its reset header - in English and German. The last check's unread list is a notice of
// its own: the versions shown are the ones from before.

async function refusedRun(page: Page, error: string, minutes?: number): Promise<void> {
    await page.route('**/api/system/v1/catalog/progress', (route) =>
        route.fulfill({
            json: {
                progress: {
                    addon_id: 'redmatic',
                    phase: 'failed',
                    message: 'GitHub rate limit, try again in 23 min: the release list of rdmtc/RedMatic cannot be read now; nothing was installed',
                    started: '2026-09-26T21:10:00Z',
                    finished: '2026-09-26T21:10:01Z',
                    percent: 2,
                    error,
                    ...(minutes ? {retry_minutes: minutes} : {}),
                },
            },
        }),
    );
}

async function uncheckedReleases(page: Page, code: string, minutes?: number): Promise<void> {
    await page.route('**/api/system/v1/catalog', async (route) => {
        const res = await route.fetch();
        const body = await res.json();
        body.catalog.releases_error = {code, repo: 'rdmtc/RedMatic', message: 'GitHub rate limit, try again in 23 min: the release list of rdmtc/RedMatic cannot be read now', at: '2026-09-26T21:10:00Z', ...(minutes ? {retry_minutes: minutes} : {})};
        await route.fulfill({response: res, json: body});
    });
}

test('a rate-limited install says so with the wait, and that nothing was installed', async ({page}) => {
    await refusedRun(page, 'github-rate-limit', 23);
    await page.goto('/addons');
    const progress = page.locator('.ol-install-progress');
    await expect(progress).toHaveAttribute('data-phase', 'failed');
    await expect(progress).toHaveClass(/error/);
    await expect(progress.locator('[data-refused="github-rate-limit"]')).toHaveText('GitHub rate limit, try again in 23 min. Nothing was installed.');
    // the page's own words, not the box's English message
    await expect(progress).not.toContainText('cannot be read now');
    expect(await fitsWindow(page)).toBe(true);
});

test('German: the rate limit and the unread check', async ({page}) => {
    await page.addInitScript(() => localStorage.setItem('ol.language', 'de'));
    await refusedRun(page, 'github-rate-limit', 5);
    await uncheckedReleases(page, 'github-rate-limit', 5);
    await page.goto('/addons');
    await expect(page.locator('[data-refused]')).toHaveText('GitHub-Ratenlimit, in 5 Min. erneut versuchen. Es wurde nichts installiert.');
    const notice = page.locator('[data-releases-error="github-rate-limit"]');
    await expect(notice).toContainText('Die letzte Prüfung konnte die Releases von rdmtc/RedMatic nicht lesen');
    await expect(notice).toContainText('GitHub-Ratenlimit, in 5 Min. erneut versuchen.');
});

test('without a wait, and without an answer at all', async ({page}) => {
    await refusedRun(page, 'releases-unreachable');
    await uncheckedReleases(page, 'github-rate-limit');
    await page.goto('/addons');
    await expect(page.locator('[data-refused="releases-unreachable"]')).toHaveText('The list of releases could not be read from GitHub. Nothing was installed.');
    const notice = page.locator('[data-releases-error]');
    await expect(notice).toContainText('The last check could not read the releases of rdmtc/RedMatic: the versions and updates shown are from before it.');
    await expect(notice).toContainText('GitHub rate limit, try again later.');
    expect(await fitsWindow(page)).toBe(true);
});

test('no notice while the last check read every list', async ({page}) => {
    await page.goto('/addons');
    await expect(page.locator('#addon-row-redmatic')).toBeVisible();
    await expect(page.locator('[data-releases-error]')).toHaveCount(0);
});
