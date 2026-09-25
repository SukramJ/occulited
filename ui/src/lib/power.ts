// The power menu's halt question depends on what the box is: after a halt, a Raspberry Pi or a
// CCU3 has no button to start it again, a virtual machine and a container are started on their
// host. The product is /VERSION's PLATFORM, which GET /system-update hands on as running.platform
// beside `container` (Root.Container: the container marker, PID 1's environment, or PLATFORM itself).

/**
 * - `board`: a single-board computer without a power button (every Raspberry Pi before the 5, the
 *   CCU3 and the Charly, whose PLATFORM is rpi3, and upstream's other boards);
 * - `board-button`: a Raspberry Pi 5, which starts again from the button on its board;
 * - `vm`: the virtual appliance;
 * - `container`: an LXC or OCI container;
 * - `unknown`: anything else (a generic x86 image may be a PC or a VM), which gets the general text.
 */
export type HaltKind = 'board' | 'board-button' | 'vm' | 'container' | 'unknown';

export function haltKind(platform: string | undefined, container: string | undefined): HaltKind {
    const p = (platform ?? '').trim().toLowerCase();
    if ((container ?? '').trim() !== '' || p === 'lxc' || p === 'oci') return 'container';
    if (p === 'ova') return 'vm';
    if (p === 'rpi5') return 'board-button';
    if (/^rpi\d*$/.test(p) || p === 'ccu3' || p === 'tinkerboard' || p.startsWith('odroid')) return 'board';
    return 'unknown';
}

/** The health route's uptime in seconds, or -1 when the box does not answer with one. */
export async function boxUptime(): Promise<number> {
    try {
        const r = await fetch('/api/system/v1/health', {cache: 'no-store'});
        const h = (await r.json()) as {uptime_s?: number};
        return typeof h.uptime_s === 'number' ? h.uptime_s : -1;
    } catch {
        return -1;
    }
}

// Waiting for the box to come back after a reboot is lib/bootwatch.ts's watchBoot, which records
// the countdown's checkpoints on the way (task 94).
