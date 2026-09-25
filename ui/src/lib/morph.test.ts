import {describe, expect, it} from 'vitest';
import {CONTENT_IN, CONTENT_OUT, LABEL_IN, LABEL_OUT, MORPH_CLOSE_MS, MORPH_OPEN_MS, boxFrame, moved, type Look} from './morph';

// task 98 (refined): the geometry of the button growing into its panel. The browser half - the rects at
// the first and the last frame, the page below - is in disclosure.spec.ts.

const button: Look = {left: 56, top: 900, width: 104, height: 24, padding: '3px 10px 3px 10px', borderRadius: '3px', backgroundColor: 'rgb(240, 240, 240)', borderColor: 'rgb(206, 206, 206)', boxShadow: 'none'};
const panel: Look = {left: 56, top: 908, width: 720, height: 230, padding: '14px 16px 14px 16px', borderRadius: '10px', backgroundColor: 'rgb(255, 255, 255)', borderColor: 'rgb(230, 230, 230)', boxShadow: 'rgba(16, 24, 40, 0.06) 0px 1px 2px 0px'};

describe('the morph', () => {
    it('opens in about a quarter of a second and closes a little faster', () => {
        expect(MORPH_OPEN_MS).toBeGreaterThanOrEqual(200);
        expect(MORPH_OPEN_MS).toBeLessThanOrEqual(250);
        expect(MORPH_CLOSE_MS).toBeLessThan(MORPH_OPEN_MS);
    });

    it('draws the panel as the button at the first frame', () => {
        // the panel's own box starts where the slot starts before its margin, 8 px above its place at rest
        const origin = {left: 56, top: 900};
        expect(boxFrame(button, origin)).toEqual({
            transform: 'translate(0px, 0px)',
            width: '104px',
            height: '24px',
            padding: '3px 10px 3px 10px',
            borderRadius: '3px',
            backgroundColor: 'rgb(240, 240, 240)',
            borderColor: 'rgb(206, 206, 206)',
            boxShadow: 'none',
        });
        // a trigger elsewhere on the page - the gateway row's Change key, 300 px up and 700 px right
        expect(boxFrame({...button, left: 756, top: 600}, origin).transform).toBe('translate(700px, -300px)');
    });

    it('ends as the panel in its own place', () => {
        expect(boxFrame(panel, panel)).toMatchObject({transform: 'translate(0px, 0px)', width: '720px', height: '230px', borderRadius: '10px'});
    });

    it('carries an offset from one reference to another', () => {
        expect(moved({left: 60, top: 910}, {left: 56, top: 908}, {left: 56, top: 900})).toEqual({left: 60, top: 902});
    });

    it('hands the label and the content over, each in order', () => {
        for (const frames of [CONTENT_IN, CONTENT_OUT, LABEL_IN, LABEL_OUT]) {
            const offsets = frames.map((f) => f.offset as number);
            expect(offsets[0]).toBe(0);
            expect(offsets.at(-1)).toBe(1);
            expect([...offsets].sort((a, b) => a - b)).toEqual(offsets);
        }
        expect([CONTENT_IN[0]!.opacity, CONTENT_IN.at(-1)!.opacity]).toEqual([0, 1]);
        expect([LABEL_OUT[0]!.opacity, LABEL_OUT.at(-1)!.opacity]).toEqual([1, 0]);
        expect([CONTENT_OUT[0]!.opacity, CONTENT_OUT.at(-1)!.opacity]).toEqual([1, 0]);
        expect([LABEL_IN[0]!.opacity, LABEL_IN.at(-1)!.opacity]).toEqual([0, 1]);
        // the label is gone before the content comes, and the content gone before the label comes back
        expect(LABEL_OUT[1]!.offset as number).toBeLessThanOrEqual(CONTENT_IN[1]!.offset as number);
        expect(CONTENT_OUT[1]!.offset as number).toBeLessThanOrEqual(LABEL_IN[1]!.offset as number);
    });
});
