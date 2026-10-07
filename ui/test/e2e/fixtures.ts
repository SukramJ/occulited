import {test as base, expect, type BrowserContext, type Page} from '@playwright/test';

// occulited task 8: the `test` of every spec whose route handlers do work of their own - a
// route.fetch to the stub, an await before the fulfill. A test may end while such a handler is still
// running (its last assertion held once the first answer was in, the page asked again after a
// click); Playwright then closes the page under the handler, and route.fetch / route.fulfill throw
// "Test ended" or "Response has been disposed" into the worker - a failure in a test that passed,
// or one that ran later (openccu-lite B-292, occulited task 3). Here every test drops its routes
// when it ends and waits for the handlers still running, before the page closes.
//
// A handler that does not finish within ROUTE_SETTLE_MS is a spec that holds a request back and
// never lets it go: that fails the test by name instead of hanging the worker.

export const ROUTE_SETTLE_MS = 10_000;

/** Drops the page's and its context's routes, and waits for the handlers that are still running. */
export async function settleRoutes(page: Page, context: BrowserContext = page.context()): Promise<void> {
    let timer: ReturnType<typeof setTimeout> | undefined;
    const late = new Promise<'late'>((resolve) => {
        timer = setTimeout(() => resolve('late'), ROUTE_SETTLE_MS);
    });
    const settled = Promise.all([page.unrouteAll({behavior: 'wait'}), context.unrouteAll({behavior: 'wait'})]).then(() => 'settled' as const);
    try {
        if ((await Promise.race([settled, late])) === 'late') {
            throw new Error(`a route handler was still running ${ROUTE_SETTLE_MS} ms after the test ended - a request held back and never released?`);
        }
    } finally {
        clearTimeout(timer);
    }
}

export const test = base.extend({
    page: async ({page}, use) => {
        await use(page);
        // a page the test closed itself has nothing left to settle
        if (!page.isClosed()) await settleRoutes(page);
    },
});

export {expect};

/**
 * occulited B-53: the shell's stream (GET /api/system/v1/stream) is held by a SharedWorker for every
 * window of the browser, and Playwright routes no request of a SharedWorker - neither page.route
 * nor context.route sees it. A spec that answers the stream itself makes the page run the stream's
 * hub in the window, as a browser without SharedWorker does (shellstream/client.ts): call this
 * before page.goto.
 */
export async function shellStreamInWindow(page: Page): Promise<void> {
    await page.addInitScript(() => {
        // the client looks for SharedWorker with typeof
        Object.defineProperty(window, 'SharedWorker', {value: undefined, configurable: true});
    });
}
