import {describe, expect, it} from 'vitest';

import {compareEntries, formatStars, httpURL, pendingUpdates, releasesProblemKind, repoURL} from './catalog';

// task 56: the catalogue cards sort by stars, ties by name, unknown counts last; the star count
// as the card writes it; the repository link with its GitHub fallback; the update count
describe('compareEntries', () => {
    const e = (id: string, name: string, stars?: number) => ({id, name, stars});
    it('puts the most stars first, ties by name, unknown counts last by name', () => {
        const list = [e('c', 'Cee', 5), e('z', 'Zed'), e('a', 'Ay', 120), e('b', 'Bee', 5), e('m', 'Em'), e('n', 'Nought', 0)];
        expect([...list].sort(compareEntries).map((x) => x.id)).toEqual(['a', 'b', 'c', 'n', 'm', 'z']);
    });
    it('compares names without regard to case', () => {
        expect(compareEntries(e('1', 'redmatic', 1), e('2', 'Mosquitto', 1))).toBeGreaterThan(0);
    });
    it('keeps the order when a filter drops entries in between', () => {
        const list = [e('a', 'Ay', 3), e('b', 'Bee', 9), e('c', 'Cee', 1)].sort(compareEntries);
        expect(list.filter((x) => x.id !== 'a').map((x) => x.id)).toEqual(['b', 'c']);
    });
});

describe('formatStars', () => {
    it('writes thousands as k with one decimal', () => {
        expect(formatStars(0)).toBe('0');
        expect(formatStars(999)).toBe('999');
        expect(formatStars(1000)).toBe('1k');
        expect(formatStars(1234)).toBe('1.2k');
        expect(formatStars(1950)).toBe('2k');
        expect(formatStars(12345)).toBe('12k');
    });
});

describe('repoURL', () => {
    it('is the repository URL, else GitHub by the release source, else nothing', () => {
        expect(repoURL({id: 'a', name: 'A', git: 'https://example.org/a', release: {github: 'x/y'}})).toBe('https://example.org/a');
        expect(repoURL({id: 'a', name: 'A', release: {github: 'rdmtc/RedMatic'}})).toBe('https://github.com/rdmtc/RedMatic');
        expect(repoURL({id: 'a', name: 'A'})).toBe('');
    });
    it('does not take a repository that is not an http(s) URL', () => {
        expect(repoURL({id: 'a', name: 'A', git: 'javascript:alert(1)', release: {github: 'x/y'}})).toBe('https://github.com/x/y');
    });
});

describe('httpURL', () => {
    it('accepts http and https and nothing else', () => {
        expect(httpURL('https://github.com/x/y/releases')).toBe(true);
        expect(httpURL('http://example.org/a.tar.gz')).toBe(true);
        expect(httpURL('javascript:alert(1)')).toBe(false);
        expect(httpURL('data:text/html,x')).toBe(false);
        expect(httpURL('/addons/x/update')).toBe(false);
        expect(httpURL('')).toBe(false);
        expect(httpURL(undefined)).toBe(false);
    });
});

describe('pendingUpdates', () => {
    it('counts the catalogue updates and the addons own checks, each addon once', () => {
        const cat = [{id: 'redmatic', update_available: true}, {id: 'mosquitto'}, {id: 'mh', update_available: false}];
        const checks = [{id: 'redmatic', info: {update_available: true}}, {id: 'hm2mqtt', info: {update_available: true}}, {id: 'jp', info: {update_available: false}}, {id: 'x'}];
        expect(pendingUpdates(cat, checks)).toEqual(['hm2mqtt', 'redmatic']);
        expect(pendingUpdates([], [])).toEqual([]);
    });
});

// B-21: the sentence for a release list the box could not read
describe('releasesProblemKind', () => {
    it('tells the rate limit with and without a wait from no answer', () => {
        expect(releasesProblemKind('github-rate-limit', 23)).toBe('rate-limit-wait');
        expect(releasesProblemKind('github-rate-limit', 0)).toBe('rate-limit');
        expect(releasesProblemKind('github-rate-limit', undefined)).toBe('rate-limit');
        expect(releasesProblemKind('releases-unreachable', 4)).toBe('unreachable');
        expect(releasesProblemKind('something-else', 4)).toBe('');
        expect(releasesProblemKind(undefined, undefined)).toBe('');
    });
});
