import {describe, expect, it} from 'vitest';
import {bootParam, bootSpanMs, durationLabel, findBoot, isEarlierBoot, kernelParam, kernelStamp, lineStamp, logPath, sourceParam, type BootInfo} from './logpage';

const ID = '4c1d2a6b0e8f4a2b9c3d5e7f8a1b2c3d';
const OLD = '7a8b9c0d1e2f3a4b5c6d7e8f9a0b1c2d';

describe('the Log page URL', () => {
    it('reads the source, and anything else is every message', () => {
        expect(sourceParam('kernel')).toBe('kernel');
        expect(sourceParam('system')).toBe('system');
        for (const raw of [null, undefined, '', 'all', 'Kernel', 'boot']) expect(sourceParam(raw)).toBe('all');
        expect([kernelParam('all'), kernelParam('system'), kernelParam('kernel')]).toEqual(['', '0', '1']);
    });

    it('writes the path: the unit not beside the kernel, the boot with every source', () => {
        expect(logPath()).toBe('/system/log');
        expect(logPath({unit: 'rfd'})).toBe('/system/log?unit=rfd');
        expect(logPath({source: 'all', unit: 'rfd', boot: OLD})).toBe(`/system/log?unit=rfd&boot=${OLD}`);
        expect(logPath({source: 'system', unit: 'rfd'})).toBe('/system/log?source=system&unit=rfd');
        expect(logPath({source: 'kernel', unit: 'rfd', boot: '-1'})).toBe('/system/log?source=kernel&boot=-1');
    });
});

describe('kernel stamps', () => {
    it('look like dmesg', () => {
        expect(kernelStamp(0)).toBe('[    0.000000]');
        expect(kernelStamp(undefined)).toBe('[    0.000000]');
        expect(kernelStamp(12_500_001)).toBe('[   12.500001]');
        expect(kernelStamp(88_608_376)).toBe('[   88.608376]');
        expect(kernelStamp(123_456_789_012)).toBe('[123456.789012]');
    });

    // B-116: the Pi 4's first kernel lines as GET /log?kernel=1 answers them since the fix (captured
    // 2026-09-13 00:27 with an uptime of 3532.42 s): the boot began at 23:28:26.58, a stamp of 0 is
    // left out of the JSON, and the wall clock is that start plus the kernel's stamp. Before the fix
    // the first one came as `time: 'Mar 13 18:08:30', monotonic_us: 8803370` - journald's reading,
    // with the image's date.
    const pi4 = [
        {time: 'Sep 12 23:28:26', timestamp: '2026-09-12T23:28:26.58+02:00', tag: 'kernel', message: 'Booting Linux on physical CPU 0x0000000000 [0x410fd083]'},
        {time: 'Sep 12 23:28:26', timestamp: '2026-09-12T23:28:26.580239+02:00', tag: 'kernel', message: 'kfence: initialized', monotonic_us: 239},
        {time: 'Sep 12 23:28:33', timestamp: '2026-09-12T23:28:33.787404+02:00', tag: 'systemd', pid: 1, message: 'Mounting POSIX Message Queue File System...', monotonic_us: 7_207_404},
    ];

    it('show a kernel line since the boot or on the wall clock, from captured lines', () => {
        expect(pi4.map((l) => lineStamp(l, true, 'boot'))).toEqual(['[    0.000000]', '[    0.000239]', '[    7.207404]']);
        expect(pi4.map((l) => lineStamp(l, true, 'wall'))).toEqual(['Sep 12 23:28:26', 'Sep 12 23:28:26', 'Sep 12 23:28:33']);
        // the system's lines always show their time, whatever the kernel's choice
        expect(lineStamp(pi4[2]!, false, 'boot')).toBe('Sep 12 23:28:33');
        // a kernel line without a time (dmesg on a box whose clock start is unknown) shows its stamp
        expect(lineStamp({time: '', monotonic_us: 1_234_567}, true, 'wall')).toBe('[    1.234567]');
    });
});

describe('boots', () => {
    const list: BootInfo[] = [
        {index: 0, boot_id: ID, first: '2025-09-12T09:01:00+02:00', last: '2025-09-12T18:00:00+02:00', current: true},
        {index: -1, boot_id: OLD, first: '2025-09-10T21:41:30+02:00', last: '2025-09-12T09:00:00+02:00', current: false},
    ];

    it('takes what the API takes from the route and drops the rest', () => {
        expect(bootParam(ID)).toBe(ID);
        expect(bootParam(ID.toUpperCase())).toBe(ID);
        expect(bootParam('4c1d2a6b-0e8f-4a2b-9c3d-5e7f8a1b2c3d')).toBe(ID);
        expect(bootParam('0')).toBe('0');
        expect(bootParam('-1')).toBe('-1');
        for (const bad of ['', null, undefined, '1', 'all', '-0', `${ID};`, '-1 --since=today']) expect(bootParam(bad)).toBe('');
    });

    it('tells an earlier boot and finds a boot by id or offset', () => {
        expect(isEarlierBoot('', ID)).toBe(false);
        expect(isEarlierBoot('0', ID)).toBe(false);
        expect(isEarlierBoot(ID, ID)).toBe(false);
        expect(isEarlierBoot(OLD, ID)).toBe(true);
        expect(isEarlierBoot('-1', ID)).toBe(true);
        expect(findBoot(list, '0')?.boot_id).toBe(ID);
        expect(findBoot(list, '-1')?.boot_id).toBe(OLD);
        expect(findBoot(list, OLD)?.index).toBe(-1);
        expect(findBoot(list, '')).toBeUndefined();
    });

    // B-114: the Pi 4's list after a trim in ram-sync - the running boot's first entry carries the
    // image's date, older than the boot before it; an offset still names the boot listed at it
    it('finds a boot by its offset whatever the dates of its entries say', () => {
        const pi4: BootInfo[] = [
            {index: 0, boot_id: ID, first: '2026-03-13T18:08:30+01:00', last: '2026-09-12T23:26:05+02:00', current: true},
            {index: -1, boot_id: OLD, first: '2026-09-12T23:18:46+02:00', last: '2026-09-12T23:22:56+02:00', current: false},
        ];
        expect(findBoot(pi4, '-1')?.boot_id).toBe(OLD);
        expect(findBoot(pi4, '0')?.boot_id).toBe(ID);
        expect(isEarlierBoot('-1', ID)).toBe(true);
        expect(findBoot(pi4, '-2')).toBeUndefined();
    });

    it('measures a boot and names durations in two units', () => {
        expect(bootSpanMs(list[0]!)).toBe((8 * 3600 + 59 * 60) * 1000);
        expect(bootSpanMs({first: list[0]!.first})).toBeNull();
        expect(durationLabel(45_000)).toBe('45 s');
        expect(durationLabel(245_000)).toBe('4 min 5 s');
        expect(durationLabel(240_000)).toBe('4 min');
        expect(durationLabel((13 * 3600 + 28 * 60 + 12) * 1000)).toBe('13 h 28 min');
        expect(durationLabel((2 * 86400 + 3 * 3600) * 1000)).toBe('2 d 3 h');
        expect(durationLabel(86400_000, {d: 'T', h: 'h', min: 'min', s: 's'})).toBe('1 T');
    });
});
