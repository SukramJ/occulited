import {describe, expect, it} from 'vitest';
import {fractions} from './holdcolumns';

describe('holdColumns (B-156)', () => {
    it("turns the header cells' widths into shares of the table", () => {
        expect(fractions([100, 300])).toEqual([0.25, 0.75]);
    });
    it('keeps a hidden column as a 0', () => {
        expect(fractions([50, 0, 150])).toEqual([0.25, 0, 0.75]);
    });
    it('measures nothing on a hidden head or an empty row', () => {
        expect(fractions([0, 0])).toBeNull();
        expect(fractions([])).toBeNull();
        expect(fractions([10, Number.NaN])).toBeNull();
    });
});
