// A host name as the daemon takes it (internal/system/netwrite.go hostnameRe): one DNS label -
// letters, digits and hyphens, 1 to 63 characters, no hyphen at either end. The welcome page's
// rename (openccu-lite task 327) checks it before it asks; the daemon checks it again.
const HOSTNAME = /^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$/;

export function validHostname(name: string): boolean {
    return HOSTNAME.test(name);
}

/** true when the page was opened under host (bare, or with any domain after it) */
export function openedAs(pageHost: string, host: string): boolean {
    const h = pageHost.toLowerCase();
    const n = host.toLowerCase();
    return n !== '' && (h === n || h.startsWith(`${n}.`));
}
