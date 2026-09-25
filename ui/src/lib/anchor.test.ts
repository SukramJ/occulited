import {describe, expect, it} from 'vitest';
import {anchoredAt} from './anchor';

// task 132: a section scrolls itself into view when the URL's anchor names it
describe('anchoredAt', () => {
    it.each([
        ['https', '#https', true],
        ['https', '', false],
        ['https', '#https-x', false],
        ['api-tokens', '#api-tokens', true],
        ['https', 'https', false],
    ])('%s at %j is %s', (id, hash, want) => {
        expect(anchoredAt(id, hash)).toBe(want);
    });
});
