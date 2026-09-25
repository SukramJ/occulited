import {describe, expect, it} from 'vitest';
import {settingsParam} from './logpage';

describe('the Log settings link', () => {
    it('opens the sheet on a tab it has, and nothing else', () => {
        expect(settingsParam('journal')).toBe('journal');
        expect(settingsParam('levels')).toBe('levels');
        expect(settingsParam('history')).toBe('history');
        for (const raw of [null, undefined, '', 'Journal', 'gear', '1']) expect(settingsParam(raw)).toBeNull();
    });
});
