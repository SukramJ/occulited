import {expect, test, type Page, type TestInfo} from '@playwright/test';

// task 85: the journal's two Status warnings - ram-sync or persistent that could not be set up, and a
// failed copy - and their way to the Journal tab of the Log settings. The stub plants them by cookie
// (stub-journal), per browser (stub-warnings=<id>), so the projects do not meet.

async function plant(page: Page, baseURL: string, info: TestInfo, which: string) {
    const id = `${info.project.name}-${info.title}`.replace(/[^A-Za-z0-9-]/g, '_');
    await page.context().addCookies(Object.entries({'stub-journal': which, 'stub-warnings': id}).map(([name, value]) => ({name, value, url: baseURL})));
}

test('a journal that could not be set up: the warning, and its way to the Journal settings', async ({page, baseURL}, info) => {
    await plant(page, baseURL!, info, 'fallback');
    await page.goto('/');
    const notice = page.locator('[data-warnings] [data-notice="journal-target"]');
    await expect(notice).toContainText(
        'The journal could not be kept on the userfs (RAM, copied to the userfs) and is in RAM until the next boot. The boot script said: the userfs target cannot be mounted, the journal stays in RAM (STORAGE=ram-sync)',
    );
    await expect(notice).toHaveAttribute('data-severity', 'warning');
    await notice.getByRole('link', {name: 'Journal settings'}).click();
    await expect(page).toHaveURL(/\/system\/log\?settings=journal$/);
    await expect(page.getByRole('dialog', {name: 'Log settings'}).getByRole('tab', {name: 'Journal'})).toHaveAttribute('aria-selected', 'true');
});

test('a failed copy: the warning says why', async ({page, baseURL}, info) => {
    await plant(page, baseURL!, info, 'copy-failed');
    await page.goto('/');
    const notice = page.locator('[data-warnings] [data-notice="journal-sync"]');
    await expect(notice).toContainText('The last copy of the journal to the userfs failed (');
    await expect(notice).toContainText('copying system@x.journal to /usr/local/var/log/journal failed (the userfs full or read-only?). Until a copy works again');
    await expect(notice.getByRole('link', {name: 'Journal settings'})).toHaveAttribute('href', /\/log\?settings=journal$/);
});
