/*
 * The App's lite-rpc client (task 193, phase 3): JSON-RPC 2.0 batches per interface with the
 * shell's session (api sends the Authorization header the paths need, D-115), the channel
 * descriptions cached per interface and channel type, the values, setValue, and the event
 * stream as an EventSource - a cookie-authenticated same-origin stream, which the gate allows.
 */
import {api} from '../api';
import {classify, splitRef, type Description, type Model, type Values} from './channels';

type Call = {method: string; params: unknown[]};
type Answer = {result?: unknown; error?: {code: number; message: string}};

/** One batch to one interface. */
export async function batch(iface: string, calls: Call[]): Promise<Answer[]> {
    if (calls.length === 0) return [];
    const body = calls.map((c, i) => ({jsonrpc: '2.0', method: c.method, params: c.params, id: i + 1}));
    const out = await api.post<Answer[] | Answer>(`/api/rpc/v1/json/${encodeURIComponent(iface)}`, body);
    return Array.isArray(out) ? out : [out];
}

export interface Channel {
    ref: string;
    iface: string;
    address: string;
    type: string;
    desc?: Description;
    model: Model;
    values: Values;
}

const descByType = new Map<string, Description>(); // iface|TYPE -> the VALUES description
const typeByRef = new Map<string, string>();

/** Loads what the tiles need: the channel type and description (cached), and the values. */
export async function loadChannels(refs: string[]): Promise<{channels: Map<string, Channel>; errors: string[]}> {
    const channels = new Map<string, Channel>();
    const errors: string[] = [];
    const byIface = new Map<string, string[]>();
    for (const ref of refs) {
        const {iface, address} = splitRef(ref);
        if (!iface) continue; // an old ref without the interface: nothing to ask
        byIface.set(iface, [...(byIface.get(iface) ?? []), address]);
    }
    for (const [iface, addrs] of byIface) {
        try {
            // 1. the types of the channels not seen yet
            const unknown = addrs.filter((a) => !typeByRef.has(`${iface}.${a}`));
            if (unknown.length) {
                const ans = await batch(iface, unknown.map((a) => ({method: 'getDeviceDescription', params: [a]})));
                unknown.forEach((a, i) => {
                    const r = ans[i]?.result as {TYPE?: string} | undefined;
                    if (r?.TYPE) typeByRef.set(`${iface}.${a}`, r.TYPE);
                });
            }
            // 2. the descriptions of the types not seen yet, one representative channel each
            const need = new Map<string, string>();
            for (const a of addrs) {
                const ty = typeByRef.get(`${iface}.${a}`);
                if (ty && !descByType.has(`${iface}|${ty}`) && !need.has(ty)) need.set(ty, a);
            }
            if (need.size) {
                const list = [...need];
                const ans = await batch(iface, list.map(([, a]) => ({method: 'getParamsetDescription', params: [a, 'VALUES']})));
                list.forEach(([ty], i) => {
                    if (ans[i]?.result) descByType.set(`${iface}|${ty}`, ans[i].result as Description);
                });
            }
            // 3. the values
            const ans = await batch(iface, addrs.map((a) => ({method: 'getParamset', params: [a, 'VALUES']})));
            addrs.forEach((a, i) => {
                const ref = `${iface}.${a}`;
                const type = typeByRef.get(ref) ?? '';
                const desc = descByType.get(`${iface}|${type}`);
                const values = (ans[i]?.result as Values | undefined) ?? {};
                channels.set(ref, {ref, iface, address: a, type, desc, model: classify(desc), values});
            });
        } catch (e) {
            errors.push(`${iface}: ${(e as Error).message}`);
        }
    }
    return {channels, errors};
}

/** setValue on one channel; the daemon's fault is the error's message. */
export async function setValue(ref: string, key: string, value: unknown): Promise<void> {
    const {iface, address} = splitRef(ref);
    const [ans] = await batch(iface, [{method: 'setValue', params: [address, key, value]}]);
    if (ans?.error) throw new Error(ans.error.message);
}

/** putParamset VALUES on one channel: several datapoints in one write (a siren's selection, duration and
 *  trigger belong together; rpc:operate, as setValue). */
export async function putValues(ref: string, values: Values): Promise<void> {
    const {iface, address} = splitRef(ref);
    const [ans] = await batch(iface, [{method: 'putParamset', params: [address, 'VALUES', values]}]);
    if (ans?.error) throw new Error(ans.error.message);
}

/** The event stream for the interfaces in use; onEvent gets (ref, key, value). */
export function openEvents(ifaces: string[], onEvent: (ref: string, key: string, value: unknown) => void): () => void {
    if (typeof EventSource === 'undefined' || ifaces.length === 0) return () => undefined;
    const q = ifaces.map((i) => `interface=${encodeURIComponent(i)}`).join('&');
    const es = new EventSource(`/api/rpc/v1/events?${q}&type=event`);
    es.addEventListener('event', (ev) => {
        try {
            const d = JSON.parse((ev as MessageEvent).data) as {interface: string; address: string; key: string; value: unknown};
            onEvent(`${d.interface}.${d.address}`, d.key, d.value);
        } catch {
            /* a torn line */
        }
    });
    return () => es.close();
}
