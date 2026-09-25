import type {JournalConfig, JournalMode, JournalStorage} from './api';

// task 85: the Journal panel's logic. Three modes - RAM only, RAM copied to the userfs at intervals
// and at shutdown (ram-sync), and persistent on the userfs - and what a switch between them does,
// which the boot script decides: onto the userfs at once (it bind-mounts and has journald flush),
// into ram-sync at once (it bind-mounts, keeps journald in RAM and starts the copies), back into RAM
// only at the next reboot (a mounted journal is not unmounted; from ram-sync the copies stop at once).

/** What a storage setting means on this product: '' is the product's default. */
export function effectiveStorage(storage: JournalStorage, defaultStorage: JournalMode): JournalMode {
    return storage === '' ? defaultStorage : storage;
}

/**
 * What saving a changed storage choice does, given where journald writes now:
 * `persistent` - the journal moves onto the userfs at once;
 * `ram-sync` - it is (or moves) into RAM and the copies to the userfs start at once;
 * `reboot` - it stays on the userfs until the next reboot and is in RAM from then on;
 * `stop-copies` - the copies stop at once (after a last one) and stay readable until the reboot;
 * `none` - the choice changes nothing about that (unchanged, or already so).
 */
export type SwitchEffect = 'none' | 'persistent' | 'ram-sync' | 'reboot' | 'stop-copies';

export function switchEffect(saved: JournalStorage, chosen: JournalStorage, defaultStorage: JournalMode, now: JournalMode): SwitchEffect {
    const want = effectiveStorage(chosen, defaultStorage);
    if (want === effectiveStorage(saved, defaultStorage)) return 'none';
    if (want === 'persistent') return now === 'persistent' ? 'none' : 'persistent';
    if (want === 'ram-sync') return now === 'ram-sync' ? 'none' : 'ram-sync';
    if (now === 'persistent') return 'reboot';
    if (now === 'ram-sync') return 'stop-copies';
    return 'none';
}

/**
 * Task 216: a USB stick as ram-sync's target, `usb:<label>/<dir>`. The label is udev's
 * (ID_FS_LABEL, `/usb/storage`'s `label_id`: spaces and odd characters made `_`), never the mount
 * point, whose number follows the plug order.
 */
export const STICK_PREFIX = 'usb:';
export const STICK_DIR = 'journal';

export function stickTarget(label: string, dir: string): string {
    return `${STICK_PREFIX}${label}/${dir.trim().replace(/^\/+|\/+$/g, '') || STICK_DIR}`;
}

export function parseStickTarget(target: string): {label: string; dir: string} | null {
    if (!target.startsWith(STICK_PREFIX)) return null;
    const rest = target.slice(STICK_PREFIX.length);
    const i = rest.indexOf('/');
    if (i <= 0) return {label: i === 0 ? '' : rest, dir: i === 0 ? rest.slice(1) : ''};
    return {label: rest.slice(0, i), dir: rest.slice(i + 1)};
}

/** A stick as `GET /usb/storage` lists it. */
export interface JournalStick {
    mount: string;
    path: string;
    device: string;
    fstype: string;
    label?: string;
    label_id?: string;
    vendor?: string;
    model?: string;
    read_only?: boolean;
    free_bytes: number;
}

/** The name a stick is shown by: its label, else vendor and model, else the device. */
export function stickName(s: JournalStick): string {
    return s.label || [s.vendor, s.model].filter(Boolean).join(' ') || s.device;
}

/** The copy intervals offered beside a free entry (SYNC_INTERVAL: 15min to 7d). */
export const SYNC_INTERVALS = ['1h', '6h', '12h', '24h'];

/** The writable part of /etc/config/journal. */
export interface JournalSettings {
    storage: JournalStorage;
    target: string;
    runtime_max_use: string;
    system_max_use: string;
    system_max_file: string;
    rate_limit_burst: string;
    sync_interval: string;
    target_max_use: string;
    target_max_age: string;
}

/**
 * The PUT body: the settings only. Never the deprecated `persist` - the API honours it only from a
 * client that sends no `storage` - and none of the figures, which are the box's to say.
 */
export function journalBody(j: JournalConfig): JournalSettings {
    return {
        storage: j.storage,
        target: j.target || 'userfs',
        runtime_max_use: j.runtime_max_use,
        system_max_use: j.system_max_use,
        system_max_file: j.system_max_file,
        rate_limit_burst: j.rate_limit_burst,
        sync_interval: j.sync_interval ?? '',
        target_max_use: j.target_max_use ?? '',
        target_max_age: j.target_max_age ?? '',
    };
}
