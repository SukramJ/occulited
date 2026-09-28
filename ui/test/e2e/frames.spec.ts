import {test, expect} from '@playwright/test';

// 28.5, the Node-RED half: a framed page of the same origin that asks for the theme the way
// Node-RED 5.0.6 does gets an answer, and the answer follows the shell's switch; the editor's
// language key on the shared origin follows the shell's language.
// The framed page is the stub's /addons/frames-probe/ (a srcdoc frame would inherit the shell's
// Content-Security-Policy, which allows no inline script - task 259; a real addon page under
// /addons/ is outside it, as this one is).

test('a framed Node-RED gets set-theme on request and on every change', async ({page}) => {
    await page.goto('/settings');
    await page.evaluate(() => {
        const f = document.createElement('iframe');
        f.src = '/addons/frames-probe/';
        document.body.appendChild(f);
    });
    await expect.poll(() => page.evaluate(() => (window as unknown as {__theme?: {type: string; payload?: {theme: string}}}).__theme)).toEqual({type: 'set-theme', payload: {theme: 'auto'}});
    await page.locator('select').nth(1).selectOption('dark');
    await expect.poll(() => page.evaluate(() => (window as unknown as {__theme?: {payload?: {theme: string}}}).__theme?.payload?.theme)).toBe('dark');
});

test('the editor-language key follows the shell language', async ({page}) => {
    await page.goto('/settings');
    const key = () => page.evaluate(() => localStorage.getItem('editor-language'));
    await expect.poll(key).toBe('en-US');
    await page.locator('select').nth(0).selectOption('de');
    await expect.poll(key).toBe('de');
});
