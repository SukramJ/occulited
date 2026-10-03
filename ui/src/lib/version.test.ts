import {describe, expect, it} from 'vitest';
import {occulitedVersion, shortVersion} from './version';

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

describe('occulitedVersion (task 9)', () => {
    const sha = 'fa42dfde1e31fb074df53220dd573ceb92642ff0';
    it('puts the commit beside a tagged version', () => {
        expect(occulitedVersion('1.0.0-dev.38', sha)).toBe('1.0.0-dev.38 (fa42dfde1)');
        expect(occulitedVersion('dev', sha)).toBe('dev (fa42dfde1)');
    });
    it('leaves a described version alone: it names the commit already', () => {
        expect(occulitedVersion('1.0.0-dev.38-5-gfa42dfd', sha)).toBe('1.0.0-dev.38-5-gfa42dfd');
        expect(occulitedVersion('1.0.0-dev.38-5-gfa42dfd-dirty', sha)).toBe('1.0.0-dev.38-5-gfa42dfd-dirty');
    });
    it('shows a version alone without a commit, and an older build by its commit', () => {
        expect(occulitedVersion('1.0.0-dev.38')).toBe('1.0.0-dev.38');
        expect(occulitedVersion('1df08bb0101038ac6eb7c08c3f3144a8a91840a1')).toBe('1df08bb01');
        expect(occulitedVersion('1df08bb0101038ac6eb7c08c3f3144a8a91840a1-hot', '')).toBe('1df08bb01-hot');
    });
});
