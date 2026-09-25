import {describe, expect, it} from 'vitest';
import {shortVersion} from './version';

describe('shortVersion (task 133)', () => {
    it('shortens a commit to nine digits and keeps a suffix', () => {
        expect(shortVersion('1df08bb0101038ac6eb7c08c3f3144a8a91840a1')).toBe('1df08bb01');
        expect(shortVersion('1df08bb0101038ac6eb7c08c3f3144a8a91840a1-hot')).toBe('1df08bb01-hot');
    });
    it('leaves anything that is not a commit as it is', () => {
        expect(shortVersion('t66-ecafbd00')).toBe('t66-ecafbd00');
        expect(shortVersion('stub')).toBe('stub');
        expect(shortVersion('1df08bb')).toBe('1df08bb');
    });
});
