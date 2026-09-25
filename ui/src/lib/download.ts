// Downloads without the session in the URL (task 125). A download is a plain navigation the
// browser makes without any header; the cookie goes along on the same host, but a session the shell
// holds as its Bearer alone (a host without the cookie, api.ts) has nothing else, and the page
// cannot see whether an HttpOnly cookie is there. So every download asks the box for a one-time
// ticket for that path (POST /api/auth/v1/ticket: single use, 60 s) and follows the link with it -
// never with the session id.
import {api} from './api';

/** A one-time ticket for one GET of path, or the session hand-over with the path "session". */
export async function ticketFor(path: string): Promise<string> {
    const r = await api.post<{ticket: string}>('/api/auth/v1/ticket', {path});
    return r.ticket;
}

/** The URL with a fresh ticket appended: what the browser is sent to for a download. */
export function withTicket(url: string, ticket: string): string {
    const u = new URL(url, 'http://box');
    u.searchParams.set('ticket', ticket);
    return u.pathname + u.search;
}

/** Follows a download link (a same-origin API path) with a one-time ticket. */
export async function download(url: string): Promise<void> {
    const ticket = await ticketFor(new URL(url, 'http://box').pathname);
    location.assign(withTicket(url, ticket));
}
