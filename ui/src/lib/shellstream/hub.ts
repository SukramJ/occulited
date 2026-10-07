/*
 * occulited B-53: one stream for the shell's live feeds, shared by every window of the browser.
 *
 * Each window held three long-lived streams (the addons' revision, the service messages, and the
 * pairing requests on the Status page or lite-rpc's events in the Control app). A browser gives
 * plain HTTP six connections per host across all its windows, so a third window did not load at
 * all; over https lighttpd's HTTP/2 allows eight streams at once on the browser's one connection,
 * and a fourth window's page waited behind them.
 *
 * The hub holds one GET /api/system/v1/stream with the topics its clients want - the windows of
 * the browser, through a SharedWorker (worker.ts), or the window alone where there is none - and
 * hands each event to the clients that want its topic and are shown. A client that is hidden gets
 * nothing; when it is shown again it gets the last event of each of its topics at once, as a
 * stream of its own would have sent on opening. With no client shown the stream is closed after a
 * moment, so a browser in the background holds no connection.
 */
import {LASTING_MAX, LASTING_MIN} from '../lasting';

export type Topic = 'addons' | 'pairing' | 'service-messages';

/** the event each topic arrives as - the one its own route sends */
export const TOPIC_EVENT: Record<Topic, string> = {addons: 'addons', pairing: 'pairing', 'service-messages': 'messages'};

export const STREAM_PATH = '/api/system/v1/stream';

/** what a client wants: its topics, whether it is shown, and the session it belongs to */
export interface Want {
    topics: Topic[];
    visible: boolean;
    /** the session: another one (a new sign-in) opens the stream anew under its cookie */
    key: string;
}

/** a message to a client */
export type ToClient = {t: 'event'; topic: Topic; data: string} | {t: 'unsupported'};

/** a message from a tab to the worker */
export type FromClient = ({t: 'want'} & Want) | {t: 'bye'};

export interface HubClient {
    post(m: ToClient): void;
}

/** the part of an EventSource the hub uses */
export interface StreamLike {
    readonly readyState: number;
    addEventListener(type: string, f: (ev: Event) => void): void;
    close(): void;
}

export interface HubEnv {
    open: (url: string) => StreamLike;
    setTimeout?: (f: () => void, ms: number) => unknown;
    clearTimeout?: (t: unknown) => void;
}

/** a change of the wanted topics is gathered this long before the stream is opened anew */
export const SETTLE = 50;
/** with no client shown, the stream stays this long (a reload, a quick switch between windows) */
export const LINGER = 3000;
/** the wait before a new attempt after the browser gave up on the stream, doubled up to RETRY_MAX
 *  (the shell's backoff, lasting.ts) */
export const RETRY_MIN = LASTING_MIN;
export const RETRY_MAX = LASTING_MAX;

const CLOSED = 2;

export function streamURL(topics: Topic[]): string {
    return `${STREAM_PATH}?topics=${topics.join(',')}`;
}

export class StreamHub {
    #env: HubEnv;
    #clients = new Map<HubClient, Want>();
    #es: StreamLike | null = null;
    /** the topics and the session the stream is (or is to be) open for; '' = none */
    #topics = '';
    #key = '';
    /** the key of the latest want that named one */
    #wantKey = '';
    #cache = new Map<Topic, string>();
    #plan: unknown = undefined; // the settle or linger timer
    #retry: unknown = undefined;
    #wait = RETRY_MIN;

    constructor(env: HubEnv) {
        this.#env = env;
    }

    #set(f: () => void, ms: number): unknown {
        return (this.#env.setTimeout ?? setTimeout)(f, ms);
    }
    #clear(t: unknown) {
        if (t !== undefined) (this.#env.clearTimeout ?? ((x: unknown) => clearTimeout(x as ReturnType<typeof setTimeout>)))(t);
    }

    /** the URL of the open stream, '' when none (for the tests and the worker's diagnostics) */
    get url(): string {
        return this.#es ? streamURL(this.#topics.split(',') as Topic[]) : '';
    }

    /** a client says what it wants now */
    update(c: HubClient, w: Want): void {
        const before = this.#clients.get(c);
        const want: Want = {topics: [...new Set(w.topics)].sort(), visible: w.visible, key: w.key};
        this.#clients.set(c, want);
        if (want.key) this.#wantKey = want.key;
        // shown, with a topic it did not get before: the last event of it at once, where the open
        // stream has one (a stream opened anew sends its own first events to everyone)
        if (want.visible && this.#es && want.key === this.#key) {
            for (const topic of want.topics) {
                const had = before?.visible && before.topics.includes(topic);
                const data = this.#cache.get(topic);
                if (!had && data !== undefined) c.post({t: 'event', topic, data});
            }
        }
        this.#replan();
    }

    /** a client is gone (its tab closed) */
    remove(c: HubClient): void {
        if (this.#clients.delete(c)) this.#replan();
    }

    /** the topics the shown clients want, sorted, and the session */
    #desired(): {topics: string; key: string} {
        const all = new Set<Topic>();
        for (const w of this.#clients.values()) if (w.visible) for (const t of w.topics) all.add(t);
        return {topics: [...all].sort().join(','), key: this.#wantKey};
    }

    #replan() {
        const d = this.#desired();
        this.#clear(this.#plan);
        this.#plan = undefined;
        const holding = this.#es !== null || this.#retry !== undefined;
        if (d.topics === '') {
            if (holding) this.#plan = this.#set(() => this.#close(), LINGER);
            return;
        }
        if (holding && d.topics === this.#topics && d.key === this.#key) return;
        this.#plan = this.#set(() => {
            this.#plan = undefined;
            this.#wait = RETRY_MIN;
            this.#open();
        }, SETTLE);
    }

    #close() {
        this.#plan = undefined;
        this.#clear(this.#retry);
        this.#retry = undefined;
        this.#es?.close();
        this.#es = null;
        this.#topics = '';
        this.#cache.clear();
    }

    #open() {
        this.#clear(this.#retry);
        this.#retry = undefined;
        this.#es?.close();
        this.#es = null;
        const d = this.#desired();
        if (d.topics === '') return this.#close();
        const topics = d.topics.split(',') as Topic[];
        if (d.key !== this.#key) this.#cache.clear();
        for (const t of [...this.#cache.keys()]) if (!topics.includes(t)) this.#cache.delete(t);
        this.#topics = d.topics;
        this.#key = d.key;
        const es = this.#env.open(streamURL(topics));
        this.#es = es;
        es.addEventListener('open', () => {
            if (this.#es === es) this.#wait = RETRY_MIN;
        });
        es.addEventListener('error', () => {
            // still CONNECTING: the browser tries again itself (a stream that ended); CLOSED: it gave
            // up (lighttpd's 502/503 while occulited restarts, a 401) - a new attempt, later each time
            if (this.#es !== es || es.readyState !== CLOSED) return;
            es.close();
            this.#es = null;
            this.#retry = this.#set(() => {
                this.#retry = undefined;
                this.#open();
            }, this.#wait);
            this.#wait = Math.min(this.#wait * 2, RETRY_MAX);
        });
        for (const topic of topics) {
            es.addEventListener(TOPIC_EVENT[topic], (ev) => {
                if (this.#es !== es) return;
                const data = String((ev as MessageEvent).data);
                this.#cache.set(topic, data);
                for (const [c, w] of this.#clients) {
                    if (w.visible && w.topics.includes(topic)) c.post({t: 'event', topic, data});
                }
            });
        }
    }
}
