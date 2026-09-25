import {describe, expect, it} from 'vitest';
import {framedByShell} from './framed';

// B-132: the shell must not render itself inside its own addon frame. `framedByShell` is what decides
// it, and it has to say no to everything but that one case - a top-level page, a cross-origin parent
// (whose location and document throw), and a same-origin page that embeds the shell but is not one.

type Win = Parameters<typeof framedByShell>[0];

function win(options: {top?: boolean; origin?: string; parentOrigin?: string; shell?: boolean; throws?: boolean}): Win {
    const self = {} as unknown;
    const parent = {
        get location(): {origin: string} {
            if (options.throws) throw new DOMException('cross-origin', 'SecurityError');
            return {origin: options.parentOrigin ?? 'https://ccu.lan'};
        },
        get document(): {querySelector(selector: string): unknown} {
            if (options.throws) throw new DOMException('cross-origin', 'SecurityError');
            return {querySelector: (s: string) => (options.shell && s === '.ol-shell' ? {} : null)};
        },
    };
    return {self, top: options.top === false ? {} : self, parent, location: {origin: options.origin ?? 'https://ccu.lan'}} as Win;
}

describe('framedByShell', () => {
    it('is false on a page that is not framed', () => {
        expect(framedByShell(win({shell: true}))).toBe(false);
    });

    it('is false when the parent is another origin, whose document throws', () => {
        expect(framedByShell(win({top: false, throws: true}))).toBe(false);
        expect(framedByShell(win({top: false, parentOrigin: 'https://elsewhere.lan', shell: true}))).toBe(false);
    });

    it('is false when the same-origin parent is not a shell', () => {
        expect(framedByShell(win({top: false, shell: false}))).toBe(false);
    });

    it('is true inside a frame of the shell itself', () => {
        expect(framedByShell(win({top: false, shell: true}))).toBe(true);
    });
});
