// Task 96 (D-64): switching HSTS off sends max-age=0 for a while instead of no header, since a browser
// forgets its entry only when it sees max-age=0 over the certificate it trusts - removing the header
// clears nothing (task 71). The pages then ask the user to open the box once by its name in every
// browser; these are the names and the deadline they show.
import type {HTTPSView} from './api';
import {isIPLiteral} from './recovery';

/**
 * The names a browser may keep HSTS for: the one this page came by, `<host>.<domain>` and the bare
 * host name - each is an entry of its own. Never an IP literal (HSTS does not apply to one) and never
 * `localhost`.
 */
export function hstsNames(pageHost: string, v: Pick<HTTPSView, 'redirect_fqdn_host' | 'redirect_fqdn_target'> | null): string[] {
    const out: string[] = [];
    for (const n of [pageHost, v?.redirect_fqdn_target ?? '', v?.redirect_fqdn_host ?? '']) {
        const name = n.trim().toLowerCase().replace(/\.$/, '');
        if (!name || isIPLiteral(name) || name === 'localhost' || out.includes(name)) continue;
        out.push(name);
    }
    return out;
}

/** Whether a browser may still remember HSTS for the box's name: on, or off and still clearing. */
export function hstsRemembered(v: Pick<HTTPSView, 'hsts' | 'hsts_clearing'>): boolean {
    return v.hsts || !!v.hsts_clearing;
}

/** The clearing's deadline; null while HSTS is not clearing or the daemon names no deadline. */
export function clearingUntil(v: Pick<HTTPSView, 'hsts_clearing' | 'hsts_clearing_until'> | null): Date | null {
    if (!v?.hsts_clearing || !v.hsts_clearing_until) return null;
    const d = new Date(v.hsts_clearing_until);
    return Number.isNaN(d.getTime()) ? null : d;
}
