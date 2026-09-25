import {expect, test} from '@playwright/test';

// task 78 (D-116): the account ladder on the Users page - the level column with its four steps,
// a change as PATCH {level}, a new account created at a level (operate by default).
test('the Users page shows and changes an account\'s level', async ({page}) => {
    await page.goto('/system/users');
    const rows = page.locator('[data-section="accounts"] tbody tr');
    await expect(rows).toHaveCount(3);
    await expect(page.locator('[data-section="accounts"] thead')).toContainText('Level');
    const monitor = rows.filter({hasText: 'monitor'}).getByRole('combobox', {name: 'Level'});
    await expect(monitor).toHaveValue('operate');
    await expect(monitor.locator('option')).toHaveText(['read', 'operate', 'configure', 'administer']);
    await expect(rows.filter({hasText: 'sebastian'}).getByRole('combobox', {name: 'Level'})).toHaveValue('administer');
    // the own account cannot be moved
    await expect(rows.filter({hasText: 'admin'}).first().getByRole('combobox', {name: 'Level'})).toBeDisabled();
    const [req] = await Promise.all([
        page.waitForRequest((r) => r.method() === 'PATCH' && r.url().endsWith('/api/auth/v1/users/monitor')),
        monitor.selectOption('configure'),
    ]);
    expect(req.postDataJSON()).toEqual({level: 'configure'});
    await expect(page.locator('.ol-notice')).toContainText('Level changed');
});

test('a new account is created at a level, operate by default', async ({page}) => {
    await page.goto('/system/users');
    await page.getByRole('button', {name: 'Add user'}).click();
    await page.getByPlaceholder('Username', {exact: true}).fill('kid');
    await page.getByPlaceholder('Password (min. 8)').fill('password-kid');
    const level = page.locator('[data-section="accounts"] .ol-toolbar').getByRole('combobox', {name: 'Level'});
    await expect(level).toHaveValue('operate');
    await level.selectOption('read');
    const [req] = await Promise.all([
        page.waitForRequest((r) => r.method() === 'POST' && r.url().endsWith('/api/auth/v1/users')),
        page.getByRole('button', {name: 'Create user'}).click(),
    ]);
    expect(req.postDataJSON()).toEqual({username: 'kid', password: 'password-kid', level: 'read'});
});
