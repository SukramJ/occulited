import {describe, expect, it} from 'vitest';
import {logPath, runParam} from './logpage';

// task 102: a run's Show log link opens the Log page on that run
describe('runParam', () => {
    it('takes a run id and drops anything else', () => {
        expect(runParam('20260912T221500-0badcafe')).toBe('20260912T221500-0badcafe');
        expect(runParam(' 20260912T221500-0badcafe ')).toBe('20260912T221500-0badcafe');
        for (const bad of [null, undefined, '', '20260912T221500', '20260912T221500-0BADCAFE', 'OCCULITE_RUN_ID=x', '../x']) {
            expect(runParam(bad)).toBe('');
        }
    });
});

describe('logPath with a run', () => {
    it('carries the run beside the other state', () => {
        expect(logPath({run: '20260912T221500-0badcafe'})).toBe('/system/log?run=20260912T221500-0badcafe');
        expect(logPath({source: 'system', boot: '0', run: '20260912T221500-0badcafe'})).toBe('/system/log?source=system&boot=0&run=20260912T221500-0badcafe');
        expect(logPath({unit: 'rfd'})).toBe('/system/log?unit=rfd');
    });
});
