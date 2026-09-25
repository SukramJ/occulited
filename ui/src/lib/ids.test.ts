import {describe, expect, it} from 'vitest';

/*
 * Task 52: no task, bug, decision or open-question id in anything a user reads (maintainer,
 * 2026-09-11: "remove all references to roadmap item ids from the ui"). Every text a user reads
 * is a key or a German text of the catalogue in i18n.svelte.ts; this test fails when one of them
 * names an id again. Comments keep theirs - they are how the code finds its history - so the
 * catalogue's whole-line comments are dropped before the search. The Go half is
 * internal/idsguard_test.go with the same pattern.
 *
 * The source is read through Vite's `?raw`, as theme.test.ts does, so the test needs no Node typings.
 */
const I18N = (import.meta.glob('./i18n.svelte.ts', {query: '?raw', import: 'default', eager: true}) as Record<string, string>)['./i18n.svelte.ts'] ?? '';

const ID = /\b(D|B|OQ|A)-\d+\b|\b[Tt]ask \d+|Aufgabe \d+/;

/** The lines of a source that name an id, whole-line comments and block comments left out. */
function idsIn(source: string): string[] {
    return source
        .replace(/\/\*[\s\S]*?\*\//g, '')
        .split('\n')
        .filter((l) => !/^\s*\/\//.test(l))
        .filter((l) => ID.test(l))
        .map((l) => l.trim());
}

describe('the user-facing texts', () => {
    it('are read from i18n.svelte.ts', () => {
        expect(I18N.length).toBeGreaterThan(10000);
    });

    // no exception left: the last one, the Services page's addon sentence, lost its (D-36) in
    // phase B of task 51
    it('name no task, bug or decision id', () => {
        expect(idsIn(I18N)).toEqual([]);
    });

    it('the search finds a planted id in a string and not in a comment', () => {
        expect(idsIn(`    'Planted (D-99).': {de: 'Gepflanzt.'},`)).toHaveLength(1);
        expect(idsIn(`    'Planted.': {de: 'Gepflanzt (Aufgabe 7).'},`)).toHaveLength(1);
        expect(idsIn(`    // the planted one (D-99, task 7)\n    'Planted.': {de: 'Gepflanzt.'},`)).toEqual([]);
        expect(idsIn(`    /* B-12 */ 'Planted.': {de: 'Gepflanzt.'},`)).toEqual([]);
        // a word that only looks like one is not an id
        expect(idsIn(`    'USB-2 and HmIP-RF': {de: 'USB-2 und HmIP-RF'},`)).toEqual([]);
    });
});
