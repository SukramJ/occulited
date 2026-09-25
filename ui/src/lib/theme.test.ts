import {describe, expect, it} from 'vitest';

/*
 * The theme's contract (D-22, task 53): every colour is a token of app.css, and every token
 * that changes with the theme is defined three times - on bare `:root` (light), under
 * `prefers-color-scheme: dark` and under `[data-theme='dark']` - so the manual switch works in
 * both directions. A token defined in only one of the dark blocks is a card that goes white
 * when the user picks "dark" by hand while the system says light; a colour spelled out in a
 * component is one that never changes with the theme at all.
 *
 * The sources are read through Vite's own glob with `?raw` rather than through node:fs, so the
 * test needs no Node typings and svelte-check reads it like every other module of src/.
 */
const RAW = import.meta.glob(['../**/*.svelte', '../**/*.ts', '../**/*.css', '!../**/*.test.ts'], {query: '?raw', import: 'default', eager: true}) as Record<string, string>;
// "../pages/StatusPage.svelte", "./Gauge.svelte" -> "pages/StatusPage.svelte", "lib/Gauge.svelte"
const sources = Object.entries(RAW).map(([k, src]) => ({rel: k.startsWith('./') ? `lib/${k.slice(2)}` : k.replace(/^\.\.\//, ''), src}));
const css = sources.find((s) => s.rel === 'app.css')?.src ?? '';

function tokensIn(block: string): Set<string> {
    return new Set([...block.matchAll(/(--hmm-[\w-]+)\s*:/g)].map((m) => m[1]!));
}
// the three kinds of block, wherever they are in the file (the copied part and the additions)
function blocks(re: RegExp): string {
    return [...css.matchAll(re)].map((m) => m[1]).join('\n');
}
const light = tokensIn(blocks(/(?:^|\n):root\s*\{([^}]*)\}/g));
const prefers = tokensIn(blocks(/:root:not\(\[data-theme='light'\]\)\s*\{([^}]*)\}/g));
const manual = tokensIn(blocks(/:root\[data-theme='dark'\]\s*\{([^}]*)\}/g));

describe('the theme tokens', () => {
    it('are read from app.css', () => {
        expect(css.length).toBeGreaterThan(1000);
        expect(sources.some((s) => s.rel === 'pages/StatusPage.svelte')).toBe(true);
    });
    it('are defined for light', () => {
        expect(light.size).toBeGreaterThan(20);
    });
    it('define the same set under prefers-color-scheme and under data-theme', () => {
        expect([...prefers].sort()).toEqual([...manual].sort());
    });
    it('define every dark token for light too', () => {
        for (const t of prefers) expect(light, t).toContain(t);
    });
    it('include the card surface and shadows of task 53 in all three', () => {
        for (const t of ['--hmm-card-bg', '--hmm-page-bg', '--hmm-shadow-card', '--hmm-shadow-card-hover', '--hmm-card-icon-bg']) {
            expect(light).toContain(t);
            expect(prefers).toContain(t);
            expect(manual).toContain(t);
        }
    });
    it('are the only tokens a component refers to', () => {
        // a reference with a fallback (`var(--hmm-x, …)`) states its own default and is not
        // held to the list, and neither is a reference that *is* such a fallback
        // (`var(--hmm-x, var(--hmm-y))`); one without has to exist, or the property silently
        // drops out
        const used = new Set<string>();
        for (const {src} of sources) {
            for (const m of src.matchAll(/(?<!,\s*)var\((--hmm-[\w-]+)\)/g)) used.add(m[1]!);
        }
        for (const t of used) expect(light, `${t} is used but not defined in app.css`).toContain(t);
    });
});

// the pages that still spell a colour out, from before the rule; the list shrinks, never grows
const LEGACY_HEX = new Set(['pages/LoginPage.svelte', 'pages/BackupPage.svelte', 'pages/NetworkPage.svelte']);

describe('the components', () => {
    it('spell no colour out (a hex literal outside app.css and the legacy list)', () => {
        for (const {rel, src} of sources) {
            if (!rel.endsWith('.svelte') || LEGACY_HEX.has(rel)) continue;
            // a hex colour in a style block or a style attribute; ids and comments are not colours
            const styles = [...src.matchAll(/<style[^>]*>([\s\S]*?)<\/style>|style="([^"]*)"|style=\{`([^`]*)`\}/g)].map((m) => m[1] ?? m[2] ?? m[3] ?? '').join('\n');
            const hex = styles.replace(/\/\*[\s\S]*?\*\//g, '').match(/#[0-9a-fA-F]{3,8}\b/g) ?? [];
            expect(hex, `${rel} spells out ${hex.join(', ')}`).toEqual([]);
        }
    });
});
