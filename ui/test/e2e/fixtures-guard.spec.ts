import {readFileSync, readdirSync} from 'node:fs';
import {expect, test} from '@playwright/test';

// occulited task 8: a spec whose route handlers fetch from the stub takes its `test` from
// ./fixtures, which drops the routes and waits for those handlers when a test ends. A new spec that
// forgets it brings back the flakes of openccu-lite B-292; this says which one.
test('every spec that fetches inside a route handler uses the shared fixture', async ({}, info) => {
    test.skip(info.project.name !== 'desktop-light', 'a check of the source files: once is enough');
    const dir = new URL('.', import.meta.url);
    const missing = readdirSync(dir)
        .filter((f) => f.endsWith('.spec.ts'))
        .filter((f) => {
            const src = readFileSync(new URL(f, dir), 'utf8');
            return /\b(route|r)\.fetch\(/.test(src) && !/^import \{[^}]*\btest\b[^}]*\} from '\.\/fixtures';$/m.test(src);
        });
    expect(missing).toEqual([]);
});
