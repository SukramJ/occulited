import {describe, expect, it} from 'vitest';
import jsQR from 'jsqr';
import {qrMatrix, qrSvg} from './qrencode';

// the code drawn into pixels with its quiet zone, then read back by the decoder the scanner uses
function decode(text: string): {data: string; version: number} | null {
    const m = qrMatrix(text);
    const scale = 4;
    const q = 4;
    const n = (m.length + 2 * q) * scale;
    const px = new Uint8ClampedArray(n * n * 4).fill(255);
    m.forEach((row, y) =>
        row.forEach((dark, x) => {
            if (!dark) return;
            for (let dy = 0; dy < scale; dy++) {
                for (let dx = 0; dx < scale; dx++) {
                    const i = (((y + q) * scale + dy) * n + (x + q) * scale + dx) * 4;
                    px[i] = px[i + 1] = px[i + 2] = 0;
                }
            }
        }),
    );
    const r = jsQR(px, n, n);
    return r ? {data: r.data, version: r.version} : null;
}

describe('qrencode', () => {
    it("draws a device's code that reads back, alphanumeric in version 4", () => {
        const payload = 'EQ01SG3014F711A0000E0000000A05DLK0123456789ABCDEFFEDCBA9876543210';
        expect(qrMatrix(payload).length).toBe(33);
        expect(decode(payload)).toEqual({data: payload, version: 4});
    });

    it('reads back every length up to version 10, in both modes', () => {
        for (let len = 1; len <= 213; len += 7) {
            const bytes = 'wifi-' + 'aZ9;é'.repeat(60);
            const text = bytes.slice(0, len);
            if (new TextEncoder().encode(text).length <= 213) expect(decode(text)?.data).toBe(text);
            const alnum = 'EQ01SG0123456789ABCDEF $%*+-./:'.repeat(12).slice(0, len);
            expect(decode(alnum)?.data).toBe(alnum);
        }
    });

    it('carries the version information from version 7 on', () => {
        const text = 'x'.repeat(130);
        const r = decode(text);
        expect(r?.data).toBe(text);
        expect(r?.version).toBeGreaterThanOrEqual(7);
    });

    it('refuses what does not fit version 10', () => {
        expect(() => qrMatrix('x'.repeat(214))).toThrow();
    });

    it('draws an SVG path with the quiet zone', () => {
        const {size, path} = qrSvg('HELLO');
        expect(size).toBe(21 + 8);
        expect(path.startsWith('M4 4h1v1h-1z')).toBe(true);
    });
});
