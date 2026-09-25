/*
 * The badges on a card (task 193): the device's maintenance channel made visible - unreachable,
 * low battery, configuration or update pending, sabotage, a fault, the duty cycle - in a fixed
 * order, at most three on the tile and the rest behind a +n. The keys are the CCU's service
 * messages; occulited's store lists the active ones for the whole house (the rollup on the
 * rooms, functions and drawer entries), the :0 channel's events keep an open page live.
 */
import type {IconName} from '../Icon.svelte';

export interface Badge {
    key: string;
    icon: IconName;
    /** the label, a t() key */
    label: string;
    /** the CSS class for the struck-through radio */
    strike?: boolean;
}

// the order is the row's order: the same problem always sits in the same place
const TABLE: {keys: RegExp; icon: IconName; label: string; strike?: boolean}[] = [
    {keys: /^(UNREACH|STICKY_UNREACH)$/, icon: 'radio', label: 'not reachable', strike: true},
    {keys: /^(LOWBAT|LOW_BAT)$/, icon: 'battery', label: 'Low battery'},
    {keys: /^CONFIG_PENDING$/, icon: 'clock', label: 'Configuration pending'},
    {keys: /^(UPDATE_PENDING|DEVICE_IN_BOOTLOADER)$/, icon: 'download', label: 'Update pending'},
    {keys: /^(SABOTAGE|ERROR_SABOTAGE)$/, icon: 'shield', label: 'Sabotage'},
    {keys: /^(ERROR|ERROR_CODE|ERROR_[A-Z_]+|FAULT_REPORTING)$/, icon: 'alert', label: 'Fault'},
    {keys: /^(DUTY_CYCLE|DUTYCYCLE)$/, icon: 'activity', label: 'Duty cycle exceeded'},
];

/** The badges for a set of active maintenance keys, in the row's order, one per kind. */
export function badgesFor(keys: Iterable<string>): Badge[] {
    const out: Badge[] = [];
    const set = new Set(keys);
    for (const row of TABLE) {
        const hit = [...set].find((k) => row.keys.test(k));
        if (hit) out.push({key: hit, icon: row.icon, label: row.label, strike: row.strike});
    }
    return out;
}

/** Whether a maintenance datapoint's value means an active message (as the store decides it). */
export function active(value: unknown): boolean {
    if (typeof value === 'boolean') return value;
    if (typeof value === 'number') return value !== 0;
    if (typeof value === 'string') return value !== '' && value !== '0' && value.toLowerCase() !== 'false';
    return false;
}

/** Whether a key of the :0 channel is a message at all (the well-known ones; RSSI and the like are not). */
export function isMaintenanceKey(key: string): boolean {
    return TABLE.some((row) => row.keys.test(key));
}

/** The device of a ref: <interface>.<address> without the channel. */
export function deviceOf(ref: string): string {
    const i = ref.lastIndexOf(':');
    return i > 0 ? ref.slice(0, i) : ref;
}
