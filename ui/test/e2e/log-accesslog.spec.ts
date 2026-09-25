import {test, expect} from '@playwright/test';

// The level dialog's lighttpd access log switch: off by default, and each way it goes into the
// PUT as lighttpd.access_log and asks for the lighttpd restart. The PUT is answered here, as the
// box answers it: what was stored, and lighttpd to restart.
test('the access log switch is off by default, and on and off each send it and offer the restart', async ({page}) => {
    const puts: {lighttpd: Record<string, boolean>}[] = [];
    await page.route('**/api/system/v1/loglevels', async (route) => {
        if (route.request().method() !== 'PUT') return route.fallback();
        const body = route.request().postDataJSON();
        puts.push(body);
        await route.fulfill({json: {...body, restart: ['lighttpd'], applied: []}});
    });
    await page.route('**/api/system/v1/services/lighttpd/restart', (route) => route.fulfill({json: {ok: true}}));
    await page.goto('/system/log');
    // task 93: the levels are the first tab of the settings sheet behind the gear
    await page.getByRole('button', {name: 'Log settings'}).click();
    await expect(page.getByRole('tab', {name: 'Log levels'})).toHaveAttribute('aria-selected', 'true');
    const sw = page.getByRole('checkbox', {name: /Access log/});
    await expect(sw).toBeVisible();
    await expect(sw).not.toBeChecked();

    await sw.check();
    await page.getByRole('button', {name: 'Save', exact: true}).click();
    await expect.poll(() => puts.length).toBe(1);
    expect(puts[0]!.lighttpd.access_log).toBe(true);
    // the debug switches travel beside it unchanged
    expect(puts[0]!.lighttpd.request_handling).toBe(false);
    const restart = page.getByRole('button', {name: 'Restart lighttpd'});
    await expect(restart).toBeVisible();
    await expect(page.getByText('lighttpd: the change shows after a restart.')).toBeVisible();
    await expect(sw).toBeChecked();
    await restart.click();
    await expect(restart).toBeHidden();

    await sw.uncheck();
    await page.getByRole('button', {name: 'Save', exact: true}).click();
    await expect.poll(() => puts.length).toBe(2);
    expect(puts[1]!.lighttpd.access_log).toBe(false);
    await expect(page.getByRole('button', {name: 'Restart lighttpd'})).toBeVisible();
    await expect(sw).not.toBeChecked();
});
