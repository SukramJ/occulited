import {expect, test} from '@playwright/test';

// B-72: on a phone the Log's message column got about ten characters - time, level and source kept
// their widths as columns. Below the phone breakpoint they are a compact head line and the message
// takes the whole width under it; a desktop keeps the columns. (The same smoke test once covered the
// Metadata tree's Path column; that page went with task 193.)

type Box = {top: number; bottom: number; left: number; right: number; width: number};

test('the Log: the message under a head line on a phone, beside it on a desktop', async ({page}, info) => {
    await page.goto('/system/log');
    const line = page.locator('.ol-line').first();
    await expect(line).toBeVisible();
    const m = await line.evaluate((l) => {
        const box = (e: Element): Box => {
            const r = e.getBoundingClientRect();
            return {top: r.top, bottom: r.bottom, left: r.left, right: r.right, width: r.width};
        };
        return {line: box(l), ts: box(l.querySelector('.ol-ts')!), lvl: box(l.querySelector('.ol-lvl')!), src: box(l.querySelector('.ol-src')!), msg: box(l.querySelector('.ol-msg')!)};
    });
    if (info.project.use.isMobile) {
        // time, level and source side by side on one line, the message below at the full width
        expect(Math.abs(m.lvl.top - m.ts.top)).toBeLessThan(4);
        expect(Math.abs(m.src.top - m.ts.top)).toBeLessThan(4);
        expect(m.src.right).toBeLessThanOrEqual(m.line.right + 1);
        expect(m.msg.top).toBeGreaterThanOrEqual(Math.max(m.ts.bottom, m.src.bottom) - 1);
        expect(m.msg.width).toBeGreaterThan(m.line.width - 2);
    } else {
        expect(Math.abs(m.msg.top - m.ts.top)).toBeLessThan(4);
        expect(m.msg.left).toBeGreaterThanOrEqual(m.src.right);
    }
});

