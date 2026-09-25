import {defineConfig, devices} from '@playwright/test';

// PORT lets two checkouts run their suites side by side; the stub reads the same variable
const PORT = Number(process.env.PORT || 8799);

// B-115: two of the four pages that were wider than a phone were so in Firefox or WebKit alone.
// Task 188 added the System menu's Ctrl/⌘+K to the same list, because Chromium and Firefox bind
// Ctrl+K to a search field of their own (system-menu-shortcut.spec.ts), and B-159 the menu's
// morph, stepped frame by frame in every engine (system-menu-morph.spec.ts), and task 162 the drag
// handles of the lists ordered by hand (sortable.spec.ts): pointer capture and
// Escape are where engines differ.
// PW_ENGINES=firefox,webkit adds those engines for those checks -
// on request, not in the default run: CI's host executor has Chromium's libraries only, and WebKit
// runs in the Playwright container on WSL (mcr.microsoft.com/playwright, the same version).
const ENGINE_DEVICES: Record<string, string> = {firefox: 'Desktop Firefox', webkit: 'Desktop Safari'};
const engines = (process.env.PW_ENGINES ?? '').split(',').map((e) => e.trim()).filter((e) => e in ENGINE_DEVICES);

// Task 14: the admin UI against the stub server (test/stub/server.mjs) - every page, both
// themes, a phone and a desktop viewport. `npm run build` first: the stub serves internal/ui/dist.
export default defineConfig({
    testDir: 'test/e2e',
    timeout: 30_000,
    fullyParallel: true,
    retries: process.env.CI ? 1 : 0,
    reporter: process.env.CI ? [['list'], ['html', {open: 'never'}]] : 'list',
    // no service worker in the suite: the shell served by the stub is the one under test, never a cached one
    use: {baseURL: `http://127.0.0.1:${PORT}`, trace: 'retain-on-failure', serviceWorkers: 'block'},
    webServer: {command: 'node test/stub/server.mjs', url: `http://127.0.0.1:${PORT}/api/system/v1/health`, env: {PORT: String(PORT)}, reuseExistingServer: !process.env.CI, timeout: 15_000},
    projects: [
        {name: 'desktop-light', use: {...devices['Desktop Chrome'], colorScheme: 'light'}},
        {name: 'desktop-dark', use: {...devices['Desktop Chrome'], colorScheme: 'dark'}},
        {name: 'phone', use: {...devices['Pixel 7'], colorScheme: 'light'}},
        ...engines.map((e) => ({name: e, testMatch: /(phone-width|system-menu-shortcut|system-menu-morph|sortable)\.spec\.ts$/, use: {...devices[ENGINE_DEVICES[e]], colorScheme: 'light' as const}})),
    ],
});
