import {describe, expect, it} from 'vitest';
import {badgesFor, isMaintenanceKey, isProblem} from './badges';

// occulited B-46: "not reachable" follows UNREACH alone; STICKY_UNREACH without it is a mild note
// about the past
describe('the reachability badges', () => {
    it('strikes the radio for UNREACH, with or without the sticky flag', () => {
        expect(badgesFor(['UNREACH'])).toEqual([{key: 'UNREACH', icon: 'radio', label: 'not reachable', strike: true, mild: undefined}]);
        // both set: one badge, the present one
        expect(badgesFor(['STICKY_UNREACH', 'UNREACH']).map((b) => b.label)).toEqual(['not reachable']);
    });
    it('shows a device that answers again as "was unreachable": mild, no strike', () => {
        expect(badgesFor(['STICKY_UNREACH'])).toEqual([{key: 'STICKY_UNREACH', icon: 'radio', label: 'was unreachable', strike: undefined, mild: true}]);
        // beside another message it keeps its place, the first in the row
        expect(badgesFor(['LOWBAT', 'STICKY_UNREACH']).map((b) => b.label)).toEqual(['was unreachable', 'Low battery']);
    });
    it('counts only what is wrong now as a problem for the rollup', () => {
        expect(isProblem(['STICKY_UNREACH'])).toBe(false);
        expect(isProblem(['STICKY_UNREACH', 'LOWBAT'])).toBe(true);
        expect(isProblem(['UNREACH', 'STICKY_UNREACH'])).toBe(true);
        expect(isProblem([])).toBe(false);
    });
    it('still takes both keys from the :0 channel\'s events', () => {
        expect(isMaintenanceKey('UNREACH')).toBe(true);
        expect(isMaintenanceKey('STICKY_UNREACH')).toBe(true);
        expect(isMaintenanceKey('RSSI_DEVICE')).toBe(false);
    });
});
