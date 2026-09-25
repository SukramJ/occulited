import {describe, expect, it} from 'vitest';

// Task 87: the icons of the Services page's row actions and menus. Icon.svelte cannot be imported
// here (node, no Svelte plugin), so its name table is read as text: every name in the IconName
// union has a path, the new ones are well-formed SVG shapes, and the svg is hidden from a screen
// reader - an icon always stands beside the words it illustrates, or beside an aria-label.
const SOURCE = (import.meta.glob('./Icon.svelte', {query: '?raw', import: 'default', eager: true}) as Record<string, string>)['./Icon.svelte'] ?? '';

function union(): string[] {
    const m = /export type IconName =([\s\S]*?);/.exec(SOURCE);
    return [...(m?.[1] ?? '').matchAll(/\|\s*'([a-z-]+)'/g)].map((x) => x[1]!);
}

function paths(): Map<string, string> {
    const out = new Map<string, string>();
    for (const m of SOURCE.matchAll(/^\s{8}'?([a-z-]+)'?: '(<[^']*>)',$/gm)) out.set(m[1]!, m[2]!);
    return out;
}

const NEW = ['play', 'stop', 'zap', 'log', 'more', 'edit', 'toggle-on', 'toggle-off', 'trash'];

describe('Icon.svelte', () => {
    it('is read', () => {
        expect(SOURCE).toContain('const PATHS');
    });

    it('has a path for every name of IconName, and no path without a name', () => {
        const names = union();
        const table = paths();
        expect(names.length).toBeGreaterThan(20);
        expect([...table.keys()].sort()).toEqual([...names].sort());
    });

    it('draws the Services page icons as closed SVG shapes', () => {
        const table = paths();
        for (const name of NEW) {
            const svg = table.get(name);
            expect(svg, name).toBeDefined();
            // nothing but self-closing shapes, each with its geometry
            const shapes = svg!.match(/<[^>]+>/g) ?? [];
            expect(shapes.length, name).toBeGreaterThan(0);
            for (const s of shapes) expect(s, `${name}: ${s}`).toMatch(/^<(path d="[^"]+"|rect( [a-z]+="[\d.]+")+|circle( [a-z]+="[\d.]+")+)\/>$/);
        }
    });

    it('fills the three dots of more, which vanish as strokes at 14 px', () => {
        expect(SOURCE).toMatch(/const FILLED = new Set<IconName>\(\[[^\]]*'more'[^\]]*\]\)/);
    });

    it('hides the svg from a screen reader and from the tab order', () => {
        const svg = /<svg[\s\S]*?>/.exec(SOURCE.slice(SOURCE.indexOf('</script>', SOURCE.indexOf('<script lang="ts">'))))?.[0] ?? '';
        expect(svg).toContain('aria-hidden="true"');
        expect(svg).toContain('focusable="false"');
    });
});
