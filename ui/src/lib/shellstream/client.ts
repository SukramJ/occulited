/*
 * occulited B-53: a window's side of the shell's shared stream (hub.ts). The pages subscribe to a
 * topic; the window tells the browser's SharedWorker which topics it wants and whether it is shown,
 * and the worker holds one stream for all windows. Where there is no SharedWorker (Chrome on
 * Android, a worker that could not be loaded, a browser without EventSource in workers) the window
 * runs the same hub itself: one stream per shown window, none while hidden.
 */
import {StreamHub, type StreamLike, type ToClient, type Topic, type Want} from './hub';

export type {Topic} from './hub';

type Listener = (data: string) => void;

/** what the client needs of the page; a test passes its own */
export interface ClientEnv {
    doc: Pick<Document, 'visibilityState' | 'addEventListener'>;
    win: Pick<Window, 'addEventListener'>;
    /** the browser's SharedWorker for the stream, or null without one */
    shared: (() => SharedWorker) | null;
    /** the window's own EventSource, for the hub it runs itself */
    open: ((url: string) => StreamLike) | null;
}

function browserEnv(): ClientEnv {
    return {
        doc: document,
        win: window,
        shared:
            typeof SharedWorker === 'undefined'
                ? null
                : () => new SharedWorker(new URL('./worker.ts', import.meta.url), {type: 'module', name: 'occulited-stream'}),
        open: typeof EventSource === 'undefined' ? null : (url) => new EventSource(url),
    };
}

export class ShellStream {
    #env: ClientEnv;
    #listeners = new Map<Topic, Set<Listener>>();
    #send: ((w: Want) => void) | null = null;
    #queued = false;
    #key = '';
    /** 'shared' or 'local' once connected (for the tests) */
    mode: '' | 'shared' | 'local' = '';

    constructor(env: ClientEnv) {
        this.#env = env;
    }

    /** f gets each event of the topic (its data, as the topic's own stream sends it) while the
     *  window is shown; the returned function ends it */
    subscribe(topic: Topic, f: Listener): () => void {
        if (!this.#send) this.#connect();
        let set = this.#listeners.get(topic);
        if (!set) this.#listeners.set(topic, (set = new Set()));
        set.add(f);
        this.#schedule();
        return () => {
            set.delete(f);
            if (set.size === 0) this.#listeners.delete(topic);
            this.#schedule();
        };
    }

    /** the session the window belongs to: another one opens the stream anew under its cookie */
    setKey(key: string): void {
        if (key === this.#key) return;
        this.#key = key;
        if (this.#send) this.#schedule();
    }

    #receive = (m: ToClient) => {
        if (m.t !== 'event') return;
        for (const f of [...(this.#listeners.get(m.topic) ?? [])]) {
            try {
                f(m.data);
            } catch (e) {
                // a listener's own error is its own: the others still get the event
                setTimeout(() => {
                    throw e;
                });
            }
        }
    };

    #want(): Want {
        return {topics: [...this.#listeners.keys()], visible: this.#env.doc.visibilityState !== 'hidden', key: this.#key};
    }

    #schedule() {
        if (this.#queued) return;
        this.#queued = true;
        queueMicrotask(() => {
            this.#queued = false;
            this.#send?.(this.#want());
        });
    }

    #local() {
        this.mode = 'local';
        const open = this.#env.open;
        if (!open) {
            this.#send = () => undefined;
            return;
        }
        const hub = new StreamHub({open});
        const me = {post: this.#receive};
        this.#send = (w) => hub.update(me, w);
        this.#schedule();
    }

    #connect() {
        this.#env.doc.addEventListener('visibilitychange', () => this.#schedule());
        let worker: SharedWorker | null = null;
        try {
            worker = this.#env.shared?.() ?? null;
        } catch {
            worker = null; // not allowed here (a sandbox): the window holds the stream itself
        }
        if (!worker) return this.#local();
        const port = worker.port;
        let gone = false;
        const fallBack = () => {
            if (gone) return;
            gone = true;
            port.close();
            this.#local();
        };
        port.addEventListener('message', (ev: MessageEvent<ToClient>) => {
            if (gone) return;
            if (ev.data?.t === 'unsupported') fallBack();
            else this.#receive(ev.data);
        });
        // the worker's script could not be loaded or run
        worker.addEventListener('error', fallBack);
        port.start();
        this.mode = 'shared';
        this.#send = (w) => {
            if (!gone) port.postMessage({t: 'want', ...w});
        };
        // the window goes (or into the back-forward cache): the worker forgets it; back from the cache it says again
        this.#env.win.addEventListener('pagehide', () => {
            if (!gone) port.postMessage({t: 'bye'});
        });
        this.#env.win.addEventListener('pageshow', (ev) => {
            if ((ev as PageTransitionEvent).persisted) this.#schedule();
        });
    }
}

let shell: ShellStream | null = null;

function instance(): ShellStream {
    return (shell ??= new ShellStream(browserEnv()));
}

/** the shell's: f gets each event of the topic while the window is shown */
export function subscribeShell(topic: Topic, f: Listener): () => void {
    return instance().subscribe(topic, f);
}

/** the shell's: the session the window belongs to (App.svelte, on every sign-in and sign-out) */
export function setShellStreamKey(key: string): void {
    instance().setKey(key);
}
