/**
 * openccu-lite B-289 (maintainer, 2026-10-02): HmIP-RF does not move onto a module whose
 * application firmware is below 2.8.0 while local key mode is off. Such a module cannot take the
 * HmIP network's key: hmipserver moves the network onto it all the same, the module cannot send,
 * and eQ-3's key server refuses every move away from it. The daemon refuses the connection change,
 * the device import and the restore (`hmip-firmware`, with the module, its version and the
 * minimum); these are the page's words for that refusal - the firmware, and the update first.
 */
export interface FirmwareRefusal {
    module: string;
    version: string;
    minimum: string;
}

type Translate = (key: string, params?: Record<string, string | number>) => string;

/** The refusal from an API error's detail, or null when it is not one. */
export function refusalOf(detail: Record<string, unknown> | undefined): FirmwareRefusal | null {
    if (!detail || typeof detail.module !== 'string' || typeof detail.version !== 'string') return null;
    return {module: detail.module, version: detail.version, minimum: typeof detail.minimum === 'string' ? detail.minimum : '2.8.0'};
}

/**
 * The refusal's paragraphs. `kind` is where it comes from: a connection change on this system
 * (local key mode can be switched on here), or a backup's import or restore (the backup's own
 * local key mode decides).
 */
export function firmwareRefusalLines(r: FirmwareRefusal, kind: 'change' | 'backup', t: Translate): string[] {
    return [
        kind === 'change'
            ? t('HmIP-RF cannot move to module {module}: it runs application firmware {version}, and below {minimum} a module cannot take the HmIP network\'s key.', {module: r.module, version: r.version, minimum: r.minimum})
            : t('The HmIP network of this backup cannot move onto module {module}: it runs application firmware {version}, and below {minimum} a module cannot take the HmIP network\'s key.', {module: r.module, version: r.version, minimum: r.minimum}),
        t('The move would leave the network on a module that cannot send to the devices, and eQ-3\'s key server would refuse every move away from it.'),
        kind === 'change'
            ? t('Update the module\'s firmware first (Updates page, radio firmware). With local key mode on, the move needs no key server and is allowed.')
            : t('Update the module\'s firmware first (Updates page, radio firmware). A backup taken in local key mode moves without the key server and is allowed.'),
    ];
}
