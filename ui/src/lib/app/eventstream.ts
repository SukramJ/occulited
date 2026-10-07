/*
 * lite-rpc's event stream read with fetch() (occulited B-47), in place of an EventSource.
 *
 * An EventSource can set no request header, and over plain http://<address>/ - no secure context,
 * so no Sec-Fetch-Site, and no Origin on a same-origin GET - a browser says nothing about where
 * the request comes from: the system refused the App's stream there ("a browser session refused"
 * in the journal at every page open) and the tiles never followed an event. fetch() sends what the
 * rest of the shell's calls send - the header credential and the session as Bearer - which a page
 * of another origin cannot.
 *
 * What an EventSource did by itself is done here: the text/event-stream parser, the reconnect with
 * Last-Event-ID (the system replays its ring, or says resync), and - which an EventSource does not
 * do - a new attempt after an answer that is not the stream (429 while a closed stream's slot is
 * not free yet, 502/503 while occulited restarts) and after 45 s of silence (the system pings
 * every 15 s: a connection that went half-open - a phone back from sleep - is given up).
 */
import {credentialHeaders} from '../api';

export interface SSEMessage {
    /** the last id the stream named: a message without one (resync) keeps the one before */
    id: string;
    event: string;
    data: string;
}

/** The text/event-stream format, fed in chunks as they arrive. */
export class SSEParser {
    #buf = '';
    #data: string[] = [];
    #event = '';
    /** the last event id seen, what a reconnect sends as Last-Event-ID */
    lastId = '';
    /** the stream's own reconnect delay (retry:), in milliseconds */
    retry: number | undefined = undefined;

    push(chunk: string): SSEMessage[] {
        this.#buf += chunk;
        const out: SSEMessage[] = [];
        for (;;) {
            // a line ends with CRLF, LF or CR; a CR at the very end may be the first half of a CRLF
            const m = /\r\n|\n|\r(?!$)/.exec(this.#buf);
            if (!m) break;
            const line = this.#buf.slice(0, m.index);
            this.#buf = this.#buf.slice(m.index + m[0].length);
            if (line === '') {
                // the blank line: the message is complete (one without data is none)
                if (this.#data.length) out.push({id: this.lastId, event: this.#event || 'message', data: this.#data.join('\n')});
                this.#data = [];
                this.#event = '';
                continue;
            }
            if (line.startsWith(':')) continue; // a comment: the system's ": ping"
            const i = line.indexOf(':');
            const field = i < 0 ? line : line.slice(0, i);
            let value = i < 0 ? '' : line.slice(i + 1);
            if (value.startsWith(' ')) value = value.slice(1);
            if (field === 'data') this.#data.push(value);
            else if (field === 'event') this.#event = value;
            else if (field === 'id' && !value.includes('\0')) this.lastId = value;
            else if (field === 'retry' && /^\d+$/.test(value)) this.retry = Number(value);
        }
        return out;
    }
}

/** The first wait before a new attempt, doubled after each attempt that brought no stream. */
export const RETRY_MIN = 1000;
export const RETRY_MAX = 30_000;
/** The system pings every 15 s; this long without a byte and the connection is taken for dead. */
export const SILENCE = 45_000;

/** Where the stream stands, for the page to say when it is not live (occulited task 19): `live` while
 *  it is open; `busy` after a 429 - the account's streams are all taken (other windows); `reconnecting`
 *  after any other answer that was no stream, a stream that ended, or a connection that failed;
 *  `refused` when it is given up (onRefused). */
export type StreamState = 'live' | 'busy' | 'reconnecting' | 'refused';

export interface EventStreamOptions {
    url: string;
    /** where to resume the first time (GET /state's event_id); afterwards the last id seen */
    lastEventId?: string;
    onMessage: (m: SSEMessage) => void;
    /** the system refused the stream in a way another attempt would not change (401, 403); nothing
     *  is tried again */
    onRefused?: (status: number) => void;
    /** told each time the stream's state changes (not before the first answer) */
    onState?: (s: StreamState) => void;
    /** for the tests */
    fetch?: typeof fetch;
}

// an answer that is not the stream and will not become it by asking again: a refusal (the
// credential, the origin rule, a bad query), or a system without lite-rpc (501). 408 and 429 pass,
// as every 5xx of the web server in front does.
function final(status: number): boolean {
    return (status >= 400 && status < 500 && status !== 408 && status !== 429) || status === 501;
}

/** Opens the stream and keeps it open until the returned function is called. */
export function openEventStream(o: EventStreamOptions): () => void {
    const doFetch = o.fetch ?? fetch;
    let closed = false;
    let ctl: AbortController | undefined;
    let wake: (() => void) | undefined;
    let waitTimer: ReturnType<typeof setTimeout> | undefined;
    let lastId = o.lastEventId ?? '';
    let first = RETRY_MIN; // the wait after a stream that ended; the stream's retry: may raise it
    let state: StreamState | undefined;
    function tell(s: StreamState) {
        if (closed && s !== 'refused') return;
        if (s === state) return;
        state = s;
        o.onState?.(s);
    }

    /** One connection: true when the stream was open and ended, false when the answer was none. */
    async function once(): Promise<boolean> {
        const abort = (ctl = new AbortController());
        const headers: Record<string, string> = {...credentialHeaders(), Accept: 'text/event-stream'};
        if (lastId) headers['Last-Event-ID'] = lastId;
        const res = await doFetch(o.url, {headers, cache: 'no-store', signal: abort.signal});
        // the stream, or an answer that is none - a refusal, the web server's own page while occulited restarts
        if (res.status !== 200 || !res.body || !(res.headers.get('Content-Type') ?? '').startsWith('text/event-stream')) {
            void res.body?.cancel().catch(() => undefined);
            if (final(res.status)) {
                closed = true;
                tell('refused');
                o.onRefused?.(res.status);
            } else tell(res.status === 429 ? 'busy' : 'reconnecting');
            return false;
        }
        tell('live');
        const parser = new SSEParser();
        parser.lastId = lastId;
        const reader = res.body.getReader();
        const text = new TextDecoder();
        let quiet = setTimeout(() => abort.abort(), SILENCE);
        try {
            for (;;) {
                const {done, value} = await reader.read();
                if (done || closed) break;
                clearTimeout(quiet);
                quiet = setTimeout(() => abort.abort(), SILENCE);
                for (const m of parser.push(text.decode(value, {stream: true}))) {
                    if (closed) break;
                    lastId = m.id;
                    try {
                        o.onMessage(m);
                    } catch (e) {
                        // a listener's own error is its own, as with an EventSource: the stream goes on
                        setTimeout(() => {
                            throw e;
                        });
                    }
                }
                lastId = parser.lastId;
                if (parser.retry !== undefined) first = Math.min(Math.max(parser.retry, RETRY_MIN), RETRY_MAX);
            }
        } finally {
            clearTimeout(quiet);
            void reader.cancel().catch(() => undefined);
        }
        return true;
    }

    async function run() {
        let delay = first;
        while (!closed) {
            let streamed = false;
            try {
                streamed = await once();
                // a stream that ended: not live until the next one is open (an answer that was no
                // stream has said why already)
                if (streamed) tell('reconnecting');
            } catch {
                // the network, or the abort after the silence: the next attempt says more
                tell('reconnecting');
            }
            if (closed) return;
            // a stream that was open and ended (a restart, the Interfaces page's x) is asked for
            // again soon; an attempt that brought none waits twice as long as the one before
            const wait = streamed ? first : delay;
            delay = streamed ? first : Math.min(delay * 2, RETRY_MAX);
            await new Promise<void>((resolve) => {
                wake = resolve;
                waitTimer = setTimeout(resolve, wait);
            });
            wake = undefined;
        }
    }
    void run();

    return () => {
        closed = true;
        ctl?.abort();
        clearTimeout(waitTimer);
        wake?.();
    };
}
