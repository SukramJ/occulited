/*
 * occulited B-53: the SharedWorker that holds the shell's stream for every window of the browser
 * (hub.ts). Each window's port says what it wants; the worker ends with the last window.
 */
import {StreamHub, type FromClient, type HubClient} from './hub';

// the SharedWorkerGlobalScope as far as it is used (the project's types are the DOM's)
declare const self: {addEventListener(type: 'connect', f: (ev: MessageEvent) => void): void};

const hub = typeof EventSource === 'undefined' ? null : new StreamHub({open: (url) => new EventSource(url)});

self.addEventListener('connect', (ev) => {
    const port = ev.ports[0]!;
    const client: HubClient = {post: (m) => port.postMessage(m)};
    if (!hub) {
        // a browser without EventSource in workers: the window holds the stream itself
        port.postMessage({t: 'unsupported'});
        return;
    }
    port.addEventListener('message', (m: MessageEvent<FromClient>) => {
        const d = m.data;
        if (d?.t === 'want') hub.update(client, {topics: d.topics, visible: d.visible, key: d.key});
        else if (d?.t === 'bye') hub.remove(client);
    });
    // a browser that tells of a port whose window went away (a crash, a closed tab without pagehide)
    port.addEventListener('close', () => hub.remove(client));
    port.start();
});
