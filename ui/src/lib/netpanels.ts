// The Network page's interface panels: which panel comes first, the state pill, the line under
// the name and the IPv6 groups are decided here; the page words them.

export interface NetAddr {
    address: string;
    prefix: number;
    /** IPv6: global, unique-local, link-local, host */
    scope?: string;
    /** IPv6: temporary, deprecated, tentative, dad-failed, permanent */
    flags?: string[];
}

export interface NetStats {
    rx_bytes: number;
    tx_bytes: number;
    rx_errors: number;
    tx_errors: number;
    rx_dropped: number;
    tx_dropped: number;
}

export interface NetIface {
    name: string;
    mac?: string;
    up: boolean;
    operstate?: string;
    carrier?: boolean;
    speed?: number;
    duplex?: string;
    mtu?: number;
    kind?: string;
    driver?: string;
    default_route?: boolean;
    ipv4?: NetAddr[];
    ipv6?: NetAddr[];
    ipv6_enabled?: boolean;
    ipv6_autoconf?: boolean;
    statistics?: NetStats;
}

export interface IPv6State {
    available: boolean;
    enabled: boolean;
    gateway?: string;
    gateway_interface?: string;
    dns: string[];
}

/** The interface carrying the default route first, the rest by name. */
export function orderInterfaces(list: NetIface[]): NetIface[] {
    return [...list].sort((a, b) => Number(!!b.default_route) - Number(!!a.default_route) || a.name.localeCompare(b.name));
}

export type LinkState = 'up' | 'no-cable' | 'down';

/** up; no cable when the kernel says there is no carrier; down otherwise (switched off). */
export function linkState(i: NetIface): LinkState {
    if (i.up) return 'up';
    if (i.carrier === false) return 'no-cable';
    return 'down';
}

export type LinkLine = {kind: 'speed'; speed: number; duplex?: string} | {kind: 'wireless'} | {kind: 'virtual'} | {kind: 'none'};

const VIRTUAL = new Set(['veth', 'virtual', 'bridge', 'vlan']);

/**
 * The line under the panel's head: the negotiated speed of a wired link, or what kind of link
 * has none. A virtual interface's speed is whatever the driver invents (a veth says 10000), so it
 * is not shown as one.
 */
export function linkLine(i: NetIface): LinkLine {
    if (i.kind === 'wireless') return {kind: 'wireless'};
    if (VIRTUAL.has(i.kind ?? '')) return {kind: 'virtual'};
    if (i.speed && i.speed > 0) return {kind: 'speed', speed: i.speed, duplex: i.duplex};
    return {kind: 'none'};
}

const SCOPE_RANK: Record<string, number> = {global: 0, 'unique-local': 1, 'link-local': 2, host: 3};

/** The IPv6 addresses grouped by scope - global, unique-local, link-local - in the kernel's order within a group. */
export function groupIPv6(addrs: NetAddr[] = []): {scope: string; addrs: NetAddr[]}[] {
    const groups = new Map<string, NetAddr[]>();
    for (const a of addrs) {
        const scope = a.scope || 'global';
        groups.set(scope, [...(groups.get(scope) ?? []), a]);
    }
    return [...groups.entries()].sort(([x], [y]) => (SCOPE_RANK[x] ?? 9) - (SCOPE_RANK[y] ?? 9)).map(([scope, list]) => ({scope, addrs: list}));
}

/** A temporary or deprecated address is shown, but muted: it is not what the box is reached by. */
export function quietAddress(a: NetAddr): boolean {
    return !!a.flags?.some((f) => f === 'temporary' || f === 'deprecated');
}

/** The flags worth a marker; permanent is the normal case and says nothing on the page. */
export function shownFlags(a: NetAddr): string[] {
    return (a.flags ?? []).filter((f) => f !== 'permanent');
}

/** The byte counters of every interface at one moment, as the page's poll received them. */
export interface CounterSample {
    /** milliseconds, from a monotonic clock (performance.now) */
    at: number;
    counters: Record<string, {rx: number; tx: number}>;
}

/** The current traffic of an interface in bits per second. */
export interface TrafficRate {
    rx: number;
    tx: number;
}

/** The counters of the interfaces that report them; one without statistics is left out. */
export function sampleOf(list: NetIface[], at: number): CounterSample {
    const counters: CounterSample['counters'] = {};
    for (const i of list) {
        if (i.statistics) counters[i.name] = {rx: i.statistics.rx_bytes, tx: i.statistics.tx_bytes};
    }
    return {at, counters};
}

/**
 * The receive and send rate per interface between two samples, in bits per second. An interface
 * missing from either sample has none, and neither has one whose counter went backwards - the
 * interface was re-created or its counters reset, and the difference means nothing.
 */
export function trafficRates(prev: CounterSample | null, next: CounterSample): Record<string, TrafficRate> {
    const out: Record<string, TrafficRate> = {};
    if (!prev) return out;
    const seconds = (next.at - prev.at) / 1000;
    if (!(seconds > 0)) return out;
    for (const [name, now] of Object.entries(next.counters)) {
        const before = prev.counters[name];
        if (!before || now.rx < before.rx || now.tx < before.tx) continue;
        out[name] = {rx: ((now.rx - before.rx) * 8) / seconds, tx: ((now.tx - before.tx) * 8) / seconds};
    }
    return out;
}

/** A rate in bits per second: one decimal below 10 of a unit, whole numbers above - 1.2 Mbit/s, 80 kbit/s. */
export function formatRate(bitsPerSecond: number): string {
    const units = ['bit/s', 'kbit/s', 'Mbit/s', 'Gbit/s'];
    let v = Math.max(0, bitsPerSecond);
    let u = 0;
    while (v >= 1000 && u < units.length - 1) {
        v /= 1000;
        u++;
    }
    if (u === 0) return `${Math.round(v)} ${units[0]}`;
    let text = v < 9.95 ? v.toFixed(1) : String(Math.round(v));
    // 999.7 kbit/s rounds to 1000: that is 1.0 Mbit/s
    if (Number(text) >= 1000 && u < units.length - 1) {
        u++;
        text = (Number(text) / 1000).toFixed(1);
    }
    return `${text} ${units[u]}`;
}

/** A byte count in SI units, one decimal from kB up. */
export function formatBytes(n: number): string {
    const units = ['B', 'kB', 'MB', 'GB', 'TB'];
    let v = n;
    let u = 0;
    while (v >= 1000 && u < units.length - 1) {
        v /= 1000;
        u++;
    }
    return u === 0 ? `${v} B` : `${v.toFixed(1)} ${units[u]}`;
}

/**
 * openccu-lite task 221: the Network page's columns - one per interface, each holding the
 * interface's panel, its IPv4 panel and its IPv6 panel. The configured (netconfig) interface
 * first, then the Wi-Fi panel where the system has a chip (also while its driver is unloaded, so
 * without a live interface), then the rest in `orderInterfaces`' order. On a phone the columns
 * stack in this order: eth0, IPv4 (eth0), IPv6 (eth0), wlan0, IPv4 (wlan0), ...
 */
export interface IfaceColumn {
    name: string;
    /** 'wifi': the Wi-Fi panel stands for the interface */
    kind: 'iface' | 'wifi';
    iface?: NetIface;
}

export function interfaceColumns(list: NetIface[], netconfig: string, wifiChip: boolean, wifiName = 'wlan0'): IfaceColumn[] {
    const ordered = orderInterfaces(list);
    const wifi = ordered.find((i) => i.kind === 'wireless');
    const rest = wifiChip ? ordered.filter((i) => i.kind !== 'wireless') : ordered;
    const first = rest.find((i) => i.name === netconfig);
    const cols: IfaceColumn[] = [];
    if (first) cols.push({name: first.name, kind: 'iface', iface: first});
    if (wifiChip) cols.push({name: wifi?.name ?? wifiName, kind: 'wifi', iface: wifi});
    for (const i of rest) if (i !== first) cols.push({name: i.name, kind: 'iface', iface: i});
    return cols;
}

/** The IPv6 default gateway, on the interface it leaves by only. */
export function ipv6GatewayOf(name: string, state?: IPv6State): string {
    return state?.gateway && state.gateway_interface === name ? state.gateway : '';
}

/**
 * openccu-lite task 226 (the maintainer, 2026-09-24: "don't show IPv4/6 panel for an
 * inactive/disabled interface"): whether an interface column shows its IPv4 and IPv6 panels.
 *
 * - The configured interface (netconfig's, eth0) always does: its IPv4 panel holds the form that
 *   sets it up, also before a cable is plugged in.
 * - Administratively down (not up, and the kernel gives no carrier at all): no.
 * - An address to show - IPv4, or IPv6 beyond link-local - : yes (a pulled cable with a static
 *   address still says what it is set to).
 * - Without such an address: no cable, Wi-Fi switched off, a virtual link (veth, bridge, vlan) or
 *   no live interface at all (the Wi-Fi driver unloaded) - no. An Ethernet or Wi-Fi interface that
 *   is up with a link but has no address yet keeps them, so a DHCP wait does not make them jump.
 */
export function showsAddressPanels(i: NetIface | undefined, o: {configured?: boolean; wifiOff?: boolean} = {}): boolean {
    if (o.configured) return true;
    if (!i) return false;
    if (!i.up && i.carrier === undefined) return false;
    const addressed = (i.ipv4?.length ?? 0) > 0 || (i.ipv6 ?? []).some((a) => (a.scope || 'global') !== 'link-local');
    if (addressed) return true;
    if (o.wifiOff || i.carrier === false || VIRTUAL.has(i.kind ?? '')) return false;
    return i.up;
}
