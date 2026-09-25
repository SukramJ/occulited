// Task 69: the Status page's storage health panel - what a device's facts mean for the rows it
// gets. The page translates; the decisions live here so they are tested.
import type {StorageDevice, StorageReport, StorageVerdict} from './api';

/**
 * Task 111: the host watches the disks' health - a VM, a container, a box without smartctl. The panel
 * says so in one quiet line; no SMART was read, so no row says it is missing.
 */
export function hostMonitored(r: Pick<StorageReport, 'health_source'>): boolean {
    return r.health_source === 'host';
}

/** The badge colour of a verdict: the kit's good, warn and bad. */
export function verdictClass(v: StorageVerdict): 'good' | 'warn' | 'bad' {
    return v === 'replace' ? 'bad' : v === 'watch' ? 'warn' : 'good';
}

/**
 * An eMMC life_time value as the range of rated life it stands for: 0x01 is 0-10 %, 0x0A is
 * 90-100 %, 0x0B is past it; 0 means the device does not say.
 */
export function emmcRange(code: number): {from: number; to: number} | 'exceeded' | null {
    if (!code || code < 0) return null;
    if (code >= 0x0b) return 'exceeded';
    return {from: (code - 1) * 10, to: code * 10};
}

/** The width of a wear bar, 0 to 100: an eMMC value's upper bound, or an SSD's percentage. */
export function barWidth(percent: number): number {
    return Math.max(0, Math.min(100, Math.round(percent)));
}

/** The level a wear figure is drawn in: from 80 % watch, from 100 % replace (the Go thresholds). */
export function wearLevel(percent: number): StorageVerdict {
    return percent >= 100 ? 'replace' : percent >= 80 ? 'watch' : 'good';
}

/**
 * What the wear and SMART rows show for a device. A virtual disk gets one note instead of both; an
 * SD card says in both rows that it has no such counter, so an empty field never reads as a
 * healthy one; an eMMC has its own estimate and no SMART; a drive shows what SMART gave, or says
 * that it gave nothing.
 */
export type WearRow = 'virtual' | 'sd' | 'emmc' | 'emmc-silent' | 'smart' | 'unreported' | 'none';
export type SmartRow = 'virtual' | 'sd' | 'value' | 'unavailable' | 'none';

export function rowsFor(d: StorageDevice): {wear: WearRow; smart: SmartRow} {
    if (d.virtual) return {wear: 'virtual', smart: 'virtual'};
    switch (d.kind) {
        case 'sd':
            return {wear: 'sd', smart: 'sd'};
        case 'emmc':
            return {wear: d.emmc ? 'emmc' : 'emmc-silent', smart: 'none'};
        case 'usb':
        case 'sata':
        case 'nvme':
        case 'other':
            if (!d.smart) return {wear: 'none', smart: 'none'};
            return {wear: d.smart.wear_percent != null ? 'smart' : 'unreported', smart: d.smart.available ? 'value' : 'unavailable'};
    }
    return {wear: 'none', smart: 'none'};
}

/** A device's identity after its kind: vendor (unless the model names it), model, capacity. */
export function identity(d: StorageDevice, formatBytes: (n: number) => string): string[] {
    const model = d.model ?? '';
    const vendor = d.vendor && !model.toLowerCase().startsWith(d.vendor.toLowerCase()) ? d.vendor : '';
    return [vendor, model, d.capacity_bytes > 0 ? formatBytes(d.capacity_bytes) : ''].filter(Boolean);
}

/** YYYY-MM as the month the card was made, MM/YYYY. */
export function madeMonth(manufactured: string): string {
    const m = /^(\d{4})-(\d{2})$/.exec(manufactured);
    return m ? `${m[2]}/${m[1]}` : manufactured;
}
