// Device firmware versions as the daemon compares them (internal/firmware: Newer): component by
// component, a missing part counts as 0, and a device that reports only major.minor - the BidCos
// devices - is compared on those two, as eQ-3's WebUI compares them (openccu-lite B-195).

function parts(v: string): number[] {
    return (v.match(/\d+/g) ?? []).map(Number);
}

/** Whether `candidate` (eQ-3's or the interface's version) beats the firmware a device runs. */
export function newerFirmware(candidate: string | undefined, running: string): boolean {
    if (!candidate) return false;
    let c = parts(candidate);
    const r = parts(running);
    if (r.length === 2 && c.length > 2) c = c.slice(0, 2);
    for (let i = 0; i < Math.max(c.length, r.length); i++) {
        const x = c[i] ?? 0;
        const y = r[i] ?? 0;
        if (x !== y) return x > y;
    }
    return false;
}

/** A version worth showing: "" for none and for the all-zero placeholder (hmipserver's AVAILABLE_FIRMWARE=0.0.0 when no bundle is deployed). */
export function shownVersion(v: string | undefined): string {
    return !v || /^0+(\.0+)*$/.test(v.trim()) ? '' : v;
}
