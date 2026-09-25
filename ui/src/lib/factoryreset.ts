/**
 * The factory reset's confirmation (task 109): the host name typed has to be this system's. Case
 * and surrounding space do not matter, and a typed FQDN passes for the short name the page shows -
 * the same rule as the API's, so a name the page lets through is one the API takes.
 */
export function sameHostname(typed: string, host: string): boolean {
    const a = typed.trim().toLowerCase();
    const b = host.trim().toLowerCase();
    if (!a || !b) return false;
    if (a === b) return true;
    const dot = a.indexOf('.');
    return dot > 0 && !b.includes('.') && a.slice(0, dot) === b;
}
