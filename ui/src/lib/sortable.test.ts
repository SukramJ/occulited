import {describe, expect, it} from 'vitest';
import {dropLine, dropTarget, edgeStep, keyTarget, moved} from './sortable';

describe('moved', () => {
    it('moves down and up', () => {
        expect(moved(['a', 'b', 'c', 'd'], 0, 2)).toEqual(['b', 'c', 'a', 'd']);
        expect(moved(['a', 'b', 'c', 'd'], 3, 1)).toEqual(['a', 'd', 'b', 'c']);
        expect(moved(['a', 'b', 'c'], 1, 1)).toEqual(['a', 'b', 'c']);
    });
    it('clamps the target and ignores a bad source', () => {
        expect(moved(['a', 'b', 'c'], 0, 9)).toEqual(['b', 'c', 'a']);
        expect(moved(['a', 'b', 'c'], 2, -4)).toEqual(['c', 'a', 'b']);
        expect(moved(['a', 'b'], 5, 0)).toEqual(['a', 'b']);
    });
});

describe('dropTarget and dropLine', () => {
    // three rows of 30 px, middles at 15, 45, 75
    const mids = [15, 45, 75];
    it('the target is the number of other rows above the pointer', () => {
        expect(dropTarget(mids, 0, 10)).toBe(0); // still on its own row
        expect(dropTarget(mids, 0, 50)).toBe(1); // past the middle of the second
        expect(dropTarget(mids, 0, 90)).toBe(2);
        expect(dropTarget(mids, 2, 10)).toBe(0);
        expect(dropTarget(mids, 2, 30)).toBe(1); // between a's middle and b's: c lands between them
        expect(dropTarget(mids, 2, 60)).toBe(2); // still its own slot: both others are above
        expect(dropTarget(mids, 2, 80)).toBe(2);
    });
    it('the line stands above the row the dragged one would push down, or below the last', () => {
        expect(dropLine(3, 0, 0)).toBe(-1);
        expect(dropLine(3, 0, 1)).toBe(2); // a lands after b: the line above c
        expect(dropLine(3, 0, 2)).toBe(3); // below the last row
        expect(dropLine(3, 2, 0)).toBe(0);
        expect(dropLine(3, 2, 1)).toBe(1);
    });
});

describe('keyTarget', () => {
    it('↑ one up, ↓ one down', () => {
        expect(keyTarget('ArrowUp', 2, 4)).toBe(1);
        expect(keyTarget('ArrowDown', 2, 4)).toBe(3);
    });
    it('nothing at either end, for another key or a row that is not there', () => {
        expect(keyTarget('ArrowUp', 0, 4)).toBe(-1);
        expect(keyTarget('ArrowDown', 3, 4)).toBe(-1);
        expect(keyTarget('ArrowLeft', 2, 4)).toBe(-1);
        expect(keyTarget('ArrowDown', 7, 4)).toBe(-1);
        expect(keyTarget('ArrowUp', 0, 0)).toBe(-1);
    });
});

describe('edgeStep', () => {
    // an area from 100 to 700, zones of 48 px, at most 18 px a frame
    it('nothing in the middle, up near the top, down near the bottom', () => {
        expect(edgeStep(400, 100, 700)).toBe(0);
        expect(edgeStep(148, 100, 700)).toBe(0);
        expect(edgeStep(652, 100, 700)).toBe(0);
        expect(edgeStep(140, 100, 700)).toBeLessThan(0);
        expect(edgeStep(660, 100, 700)).toBeGreaterThan(0);
    });
    it('faster nearer the edge, and at most the maximum beyond it', () => {
        expect(Math.abs(edgeStep(110, 100, 700))).toBeGreaterThan(Math.abs(edgeStep(140, 100, 700)));
        expect(edgeStep(100, 100, 700)).toBe(-18);
        expect(edgeStep(0, 100, 700)).toBe(-18);
        expect(edgeStep(900, 100, 700)).toBe(18);
    });
    it('a low area scrolls from its halves', () => {
        expect(edgeStep(110, 100, 160)).toBeLessThan(0);
        expect(edgeStep(150, 100, 160)).toBeGreaterThan(0);
    });
});
