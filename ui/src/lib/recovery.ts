// The recovery system by the box's address (D-56).
//
// The recovery system and a system update's install phase serve plain HTTP only. A browser that
// remembers HSTS for the box's name upgrades http://<name>/ to https:// before the request leaves
// it, so the recovery is "connection refused" by name in every browser, while http://<IP>/ still
// works: HSTS never applies to an IP literal (RFC 6797 §8.1.1). So wherever the UI sends the
// browser into the recovery - the power menu, a system update's install, the HSTS question - it
// uses an address chosen here, resolved when the dialog opens and never cached from page load:
//
//   1. the host the browser already uses, when location.hostname is an IP literal;
//   2. otherwise the IPv4 address of the interface with the default route (another interface's
//      when that one has none);
//   3. an IPv6 address, in brackets, only when the box has no IPv4 at all;
//   4. the name as a last resort, which the pages word with a warning.

import {orderInterfaces, type IPv6State, type NetAddr, type NetIface} from './netpanels';

export type AddressKind = 'literal' | 'ipv4' | 'ipv6' | 'name';

export interface BoxAddress {
    /** literal: the browser came by address already; ipv4, ipv6: from the network API; name: no address could be read */
    kind: AddressKind;
    /** the URL's host: an IPv6 address in brackets */
    host: string;
    /** http://<host>/ */
    url: string;
}

/** The part of GET /api/system/v1/network the choice reads. */
export interface NetworkView {
    interfaces?: NetIface[];
    ipv6_state?: IPv6State;
}

/** How long a dialog waits for the network API before it falls back to the name. */
export const LOOKUP_TIMEOUT_MS = 4000;

const IPV4 = /^(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(?:\.(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}$/;

export function isIPv4(s: string): boolean {
    return IPV4.test(s);
}

/** An IPv4 address, or an IPv6 one bare or in brackets (location.hostname carries the brackets). */
export function isIPLiteral(hostname: string): boolean {
    const h = hostname.trim();
    if (isIPv4(h)) return true;
    const inner = h.startsWith('[') && h.endsWith(']') ? h.slice(1, -1) : h;
    return inner.includes(':') && /^[0-9a-f:.]+$/i.test(inner);
}

function hostOf(address: string): string {
    return address.includes(':') && !address.startsWith('[') ? `[${address}]` : address;
}

export function urlOf(host: string): string {
    return `http://${host}/`;
}

// neither the loopback nor 169.254/16, the address a DHCP client gives itself when nobody answered
function usableV4(a: NetAddr): boolean {
    return isIPv4(a.address) && !a.address.startsWith('127.') && !a.address.startsWith('169.254.');
}

// a temporary address is a privacy address that changes; the others are not usable yet or any more
const V6_UNSTABLE = new Set(['temporary', 'deprecated', 'tentative', 'dad-failed']);

// 2 for a global address, 1 for a unique-local one, 0 for anything a URL cannot use: a link-local
// address needs a zone id, which browsers do not accept in a URL
function v6Rank(a: NetAddr): number {
    const addr = a.address.toLowerCase();
    if (!addr.includes(':') || (a.flags ?? []).some((f) => V6_UNSTABLE.has(f))) return 0;
    const scope = a.scope ?? (addr === '::1' ? 'host' : /^fe[89ab]/.test(addr) ? 'link-local' : /^f[cd]/.test(addr) ? 'unique-local' : 'global');
    return scope === 'global' ? 2 : scope === 'unique-local' ? 1 : 0;
}

/** The address the browser is sent to for the recovery system; see the rules at the top. */
export function boxAddress(hostname: string, network?: NetworkView | null): BoxAddress {
    const h = hostname.trim();
    if (isIPLiteral(h)) {
        const host = hostOf(h);
        return {kind: 'literal', host, url: urlOf(host)};
    }
    // the default route's interface first, the others by name
    const up = orderInterfaces(network?.interfaces ?? []).filter((i) => i.up !== false);
    for (const i of up) {
        const a = (i.ipv4 ?? []).find(usableV4);
        if (a) return {kind: 'ipv4', host: a.address, url: urlOf(a.address)};
    }
    // IPv6 only on a box without IPv4: the interface of the IPv6 default route first
    const gw = network?.ipv6_state?.gateway_interface;
    const v6order = gw ? [...up.filter((i) => i.name === gw), ...up.filter((i) => i.name !== gw)] : up;
    for (const i of v6order) {
        let best: NetAddr | null = null;
        let rank = 0;
        for (const a of i.ipv6 ?? []) {
            const r = v6Rank(a);
            if (r > rank) {
                best = a;
                rank = r;
            }
        }
        if (best) {
            const host = hostOf(best.address);
            return {kind: 'ipv6', host, url: urlOf(host)};
        }
    }
    return {kind: 'name', host: h, url: urlOf(h)};
}

/** A promise that settles with `fallback` when `p` has not settled within `ms`, or fails. */
export async function withTimeout<T>(p: Promise<T>, ms: number, fallback: T): Promise<T> {
    let timer: ReturnType<typeof setTimeout> | undefined;
    const late = new Promise<T>((resolve) => {
        timer = setTimeout(() => resolve(fallback), ms);
    });
    try {
        return await Promise.race([p.catch(() => fallback), late]);
    } finally {
        clearTimeout(timer);
    }
}

/**
 * Resolves the address when a dialog opens: an IP literal needs no lookup; otherwise `load` asks
 * the network API, and an answer that fails or does not come in time leaves the name.
 */
export async function lookupBoxAddress(hostname: string, load: () => Promise<{network?: NetworkView} | null | undefined>, timeoutMs = LOOKUP_TIMEOUT_MS): Promise<BoxAddress> {
    if (isIPLiteral(hostname)) return boxAddress(hostname);
    const answer = await withTimeout(Promise.resolve().then(load), timeoutMs, null);
    return boxAddress(hostname, answer?.network ?? null);
}

/**
 * The name as a second link beside the address: only while HSTS is known to be off (`false`; an
 * unknown state is `null` and counts as on), and only when the address is not already the host the
 * browser uses or the name itself. A browser may still remember HSTS from an earlier period, so
 * the pages word it as "unless this browser still remembers HSTS for it".
 */
export function nameLink(address: BoxAddress, hostname: string, hsts: boolean | null): string {
    if (hsts !== false || address.kind === 'literal' || address.kind === 'name' || hostname.trim() === '') return '';
    return urlOf(hostname.trim());
}
