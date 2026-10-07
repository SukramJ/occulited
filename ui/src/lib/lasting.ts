/*
 * occulited task 19: an EventSource reconnects by itself after a stream that ended, but gives up
 * for good - readyState CLOSED - when the answer to a reconnect is not the stream. That is what a
 * restart of occulited looks like behind lighttpd: the stream ends, the reconnect a few seconds
 * later finds no occulited and gets lighttpd's 502/503. The shell's streams are opened again after
 * 1 s and then twice as long each time, up to 30 s; a stream that opened starts the wait from 1 s
 * again. Since occulited B-53 the shell's stream (shellstream/hub.ts) and the Log page's follow
 * do it; lastingEventSource, which did it for each stream of a page, is gone with those streams.
 */

export const LASTING_MIN = 1000;
export const LASTING_MAX = 30_000;
