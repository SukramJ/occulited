import {afterEach, describe, expect, it, vi} from 'vitest';
import {CLOSE_MS, OPEN_MS, reveal} from './reveal';

// task 98: the one motion of the parts that open in the page. The browser half - the height growing,
// the content below moving along, focus - is in the Playwright suite (disclosure.spec.ts); here the
// transition's own arithmetic, with getComputedStyle and matchMedia stood in for.

const box = {height: '120px', paddingTop: '14px', paddingBottom: '14px', marginTop: '8px', marginBottom: '12px', borderTopWidth: '1px', borderBottomWidth: '1px'};

function stub(reduce: boolean) {
    vi.stubGlobal('getComputedStyle', () => box);
    vi.stubGlobal('matchMedia', (q: string) => ({matches: reduce && q.includes('prefers-reduced-motion: reduce')}));
}

/** the css string as the keyframe Svelte makes of it: split at `;`, stopping at an empty part */
function keyframe(css: string): Record<string, string> {
    const out: Record<string, string> = {};
    for (const part of css.split(';')) {
        const [k, v] = part.split(':');
        if (!k || v === undefined) break;
        out[k.trim()] = v.trim();
    }
    return out;
}

afterEach(() => vi.unstubAllGlobals());

describe('reveal', () => {
    it('opens in 200 ms and closes in 150 ms', () => {
        expect(OPEN_MS).toBe(200);
        expect(CLOSE_MS).toBe(150);
        stub(false);
        expect(reveal({} as Element).duration).toBe(200);
        expect(reveal({} as Element, {duration: CLOSE_MS}).duration).toBe(150);
    });

    it('grows every vertical dimension from nothing and fades in', () => {
        stub(false);
        const css = reveal({} as Element).css!;
        expect(keyframe(css(0, 1))).toEqual({
            overflow: 'clip',
            'min-height': '0',
            opacity: '0',
            height: '0px',
            'padding-top': '0px',
            'padding-bottom': '0px',
            'margin-top': '0px',
            'margin-bottom': '0px',
            'border-top-width': '0px',
            'border-bottom-width': '0px',
        });
        const half = keyframe(css(0.5, 0.5));
        expect(half.height).toBe('60px');
        expect(half['margin-bottom']).toBe('6px');
        expect(half.opacity).toBe('0.5');
        // every part reaches Svelte: none is lost behind an empty one
        expect(Object.keys(keyframe(css(1, 0)))).toHaveLength(10);
        expect(keyframe(css(1, 0)).height).toBe('120px');
    });

    it('clips without hidden, so no margin is caught inside for the length of the motion', () => {
        stub(false);
        expect(reveal({} as Element).css!(0.3, 0.7)).not.toContain('hidden');
    });

    it('does not move at all when the reader asked for less motion', () => {
        stub(true);
        const r = reveal({} as Element);
        expect(r.duration).toBe(0);
        expect(r.css).toBeUndefined();
    });
});
