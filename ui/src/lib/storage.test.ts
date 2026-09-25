import {describe, expect, it} from 'vitest';
import type {StorageDevice} from './api';
import {barWidth, emmcRange, hostMonitored, identity, madeMonth, rowsFor, verdictClass, wearLevel} from './storage';
import {formatBytes} from './netpanels';

const base: StorageDevice = {name: 'mmcblk0', kind: 'sd', capacity_bytes: 63864569856, mounts: ['/'], filesystems: [], io_errors: 0, writes: {since_boot_bytes: 0}, verdict: 'good', reasons: []};

describe('the storage panel', () => {
    it('colours the verdicts', () => {
        expect(verdictClass('good')).toBe('good');
        expect(verdictClass('watch')).toBe('warn');
        expect(verdictClass('replace')).toBe('bad');
    });

    it('reads an eMMC life_time value as a range', () => {
        expect(emmcRange(0)).toBeNull();
        expect(emmcRange(0x01)).toEqual({from: 0, to: 10});
        expect(emmcRange(0x02)).toEqual({from: 10, to: 20});
        expect(emmcRange(0x09)).toEqual({from: 80, to: 90});
        expect(emmcRange(0x0a)).toEqual({from: 90, to: 100});
        expect(emmcRange(0x0b)).toBe('exceeded');
    });

    it('draws wear in the thresholds of the verdict', () => {
        expect(barWidth(-3)).toBe(0);
        expect(barWidth(83.4)).toBe(83);
        expect(barWidth(140)).toBe(100);
        expect(wearLevel(79)).toBe('good');
        expect(wearLevel(80)).toBe('watch');
        expect(wearLevel(100)).toBe('replace');
    });

    it('gives an SD card its two notes, a virtual disk one, an eMMC its own estimate', () => {
        expect(rowsFor(base)).toEqual({wear: 'sd', smart: 'sd'});
        expect(rowsFor({...base, kind: 'virtio', virtual: true})).toEqual({wear: 'virtual', smart: 'virtual'});
        // a QEMU disk on the SATA bus is virtual too
        expect(rowsFor({...base, kind: 'sata', virtual: true, smart: {available: true, passed: true, read: ''}})).toEqual({wear: 'virtual', smart: 'virtual'});
        expect(rowsFor({...base, kind: 'emmc', emmc: {life_time_a: 2, life_time_b: 1, pre_eol: 'warning'}})).toEqual({wear: 'emmc', smart: 'none'});
        expect(rowsFor({...base, kind: 'emmc'})).toEqual({wear: 'emmc-silent', smart: 'none'});
    });

    it('shows what SMART gave a drive, and says when it gave nothing', () => {
        expect(rowsFor({...base, kind: 'nvme', smart: {available: true, passed: true, wear_percent: 83, read: ''}})).toEqual({wear: 'smart', smart: 'value'});
        expect(rowsFor({...base, kind: 'sata', smart: {available: true, passed: true, read: ''}})).toEqual({wear: 'unreported', smart: 'value'});
        expect(rowsFor({...base, kind: 'usb', smart: {available: false, message: 'Unknown USB bridge', read: ''}})).toEqual({wear: 'unreported', smart: 'unavailable'});
        // not read at all (a development root)
        expect(rowsFor({...base, kind: 'usb'})).toEqual({wear: 'none', smart: 'none'});
    });

    it('says the host monitors the disks only when the report says so (task 111)', () => {
        expect(hostMonitored({health_source: 'host'})).toBe(true);
        expect(hostMonitored({health_source: 'device'})).toBe(false);
        // a daemon from before the field reads its own disks
        expect(hostMonitored({})).toBe(false);
        // the host's disk a container lists had no SMART read: no row says one is missing
        expect(rowsFor({...base, kind: 'sata'})).toEqual({wear: 'none', smart: 'none'});
    });

    it('names a device without saying the vendor twice', () => {
        expect(identity({...base, vendor: 'SanDisk', model: 'SN64G'}, formatBytes)).toEqual(['SanDisk', 'SN64G', '63.9 GB']);
        expect(identity({...base, kind: 'virtio', vendor: 'QEMU', model: 'QEMU HARDDISK', capacity_bytes: 34359738368}, formatBytes)).toEqual(['QEMU HARDDISK', '34.4 GB']);
        expect(identity({...base, model: 'WD Blue SN570 500GB', capacity_bytes: 0}, formatBytes)).toEqual(['WD Blue SN570 500GB']);
        expect(madeMonth('2024-06')).toBe('06/2024');
        expect(madeMonth('06/2024')).toBe('06/2024');
    });
});
