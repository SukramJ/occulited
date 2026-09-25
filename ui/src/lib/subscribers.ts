// The Interfaces page's subscriber rows (task 76): which client a registration belongs to, where
// its id tells it, and the process address without the scheme.

/** The kinds of client an id names; '' when it names none the page knows. */
export type ClientKind = 'node-red' | 'hmm' | 'java' | 'rega' | '';

/**
 * The client behind a subscriber id, by the conventions described in internal/system/handlers.go:
 * `nr_…` node-red-contrib-ccu, `hmm_…` Homematic Manager, `…_java` the box's own Java server, a
 * bare number ReGa (its process id).
 */
export function clientKind(id: string): ClientKind {
    if (id.startsWith('nr_')) return 'node-red';
    if (id.startsWith('hmm_')) return 'hmm';
    if (id.endsWith('_java')) return 'java';
    if (/^\d+$/.test(id)) return 'rega';
    return '';
}

/** `xmlrpc_bin://127.0.0.1:32001` → `127.0.0.1:32001`; a path stays, a trailing slash goes. */
export function processAddress(url: string): string {
    const i = url.indexOf('://');
    return (i >= 0 ? url.slice(i + 3) : url).replace(/\/$/, '');
}

/**
 * Task 76's follow-up (D-64): whether a subscriber's callback accepted a TCP connection when the page
 * opened (GET /radio/subscribers/reachability). `reachable` is absent for a URL with no address to
 * connect to; `reason` says why not.
 */
export interface SubscriberReach {
    interface: string;
    id: string;
    url: string;
    reachable?: boolean;
    reason?: 'refused' | 'timeout' | 'unreachable';
}

/** The key a verdict is kept under: an id and its callback are unique per interface. */
export function reachKey(iface: string, id: string, url: string): string {
    return `${iface}\n${id}\n${url}`;
}

/** The process cards' order: the interfaces with a module card first, in the modules' order, then the rest as listed. */
export function processOrder<T extends {name: string}>(interfaces: T[], protocols: string[]): T[] {
    const first = protocols.map((p) => interfaces.find((i) => i.name === p)).filter((i): i is T => i !== undefined);
    return [...first, ...interfaces.filter((i) => !first.includes(i))];
}
