// openccu-lite B-201: an interface process held by a callback listener that takes its call and
// never answers - hmipserver then delivers no event to any client, rfd answers no client at all.
// occulited notices it, asks every listener the daemon is connected to (GET
// /radio/subscribers/stalls), and names the one that does not answer; the drop ends the daemon's
// connection to it (POST /radio/subscribers/drop). The Status page's warning and the Interfaces
// page say it in the same words. Task 234 adds the listener hmipserver's own log names: its
// registration finished, it took an event and does not answer, and hmipserver holds its events
// (and only its) and logs that every second - verdict blocked, kind listener.

/** one listener the check asked */
export interface StallListener {
    /** host:port */
    address: string;
    /** the handlers file's entry; absent when the listener is not in it (its registration never finished) */
    id?: string;
    url?: string;
    /** the interface process holds a connection to it right now */
    connected: boolean;
    local: boolean;
    /** the user its listening socket belongs to, for a listener on this system (an addon's user) */
    owner?: string;
    /**
     * a registered listener: answers, no-answer, refused or unreachable (asked with its own id); one
     * the process is connected to that is not on its list is never asked - holds when every
     * registered one answers (it is the one the process waits on), unregistered otherwise
     */
    verdict: 'answers' | 'no-answer' | 'refused' | 'unreachable' | 'holds' | 'unregistered' | 'blocked';
    /** task 234, verdict blocked: how long hmipserver's log says the listener's event pool has been held, in seconds */
    blocked_for?: number;
}

export interface Stall {
    interface: string;
    /**
     * delivery: calls answer, no event comes (hmipserver); calls: nothing answers (rfd); listener
     * (task 234): hmipserver's log names a listener that does not take its events - the others
     * get theirs
     */
    kind: 'delivery' | 'calls' | 'listener';
    since: string;
    /** absent while the first check runs */
    checked_at?: string;
    listeners: StallListener[];
}

export type Translate = (key: string, params?: Record<string, string | number>) => string;

/** the listeners that hold the process */
export function stuck(s: Pick<Stall, 'listeners'>): StallListener[] {
    return (s.listeners ?? []).filter((l) => l.verdict === 'no-answer' || l.verdict === 'holds' || l.verdict === 'blocked');
}

/** a listener as a person finds it: its id where the process has one, its address, the addon's user */
export function listenerName(l: StallListener, t: Translate): string {
    const base = l.id ? (l.address ? `${l.id} (${l.address})` : l.id) : l.address;
    return l.owner ? t('{name}, user {owner}', {name: base, owner: l.owner}) : base;
}

/** the sentence: what the process no longer does, and who holds it */
export function stallSentence(s: {interface: string; kind: string; checked: boolean; stuck: StallListener[]}, t: Translate): string {
    const iface = s.interface;
    const names = s.stuck.map((l) => listenerName(l, t)).join('; ');
    if (s.kind === 'listener' && s.stuck.length > 0) {
        const who =
            s.stuck.length === 1
                ? t('{interface}: the client {listeners} does not take its events. {interface} keeps them back for it and writes a warning to its log every second; the other clients get theirs. Ending that connection releases it.', {interface: iface, listeners: names})
                : t('{interface}: the clients {listeners} do not take their events. {interface} keeps them back for them and writes a warning to its log every second; the other clients get theirs. Ending their connections releases them.', {interface: iface, listeners: names});
        return s.stuck.some((l) => !l.address) ? `${who} ${t('A client that is no longer on the list of clients cannot be ended here; a restart of the interface process on the Services page ends it.')}` : who;
    }
    if (s.stuck.length > 0) {
        const what = s.kind === 'calls' ? t('{interface} answers no client any more.', {interface: iface}) : t('{interface} delivers no events to any client.', {interface: iface});
        const who =
            s.stuck.length === 1
                ? t('The client at {listeners} takes its calls and never answers, and that holds it for everybody. Ending that connection releases it.', {listeners: names})
                : t('The clients at {listeners} take its calls and never answer, and that holds it for everybody. Ending their connections releases it.', {listeners: names});
        return `${what} ${who}`;
    }
    if (!s.checked) return t('{interface} delivers no events to any client. The system is looking for the client that holds it.', {interface: iface});
    return t('{interface} delivers no events to any client, and every registered client answers. A restart of the interface process on the Services page ends it.', {interface: iface});
}
