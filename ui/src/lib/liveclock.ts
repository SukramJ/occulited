/*
 * openccu-lite task 225 (the maintainer, 2026-09-24: "the clock displays on system-networks time
 * panel should be live updated and count seconds"): the Time panel's clocks tick.
 *
 * The system time is the answer of GET /time plus the time since that answer, measured with the
 * browser's monotonic clock (performance.now()), so a change of the browser's own clock does not
 * move it; every re-read of the time anchors it again, so a drift or an NTP step on the system shows.
 * The browser's time is Date.now() itself.
 *
 * The ticker runs one interval aligned to the next full second of the browser's clock and stops
 * while it is paused (a hidden tab, a page that is not shown) or stopped (the panel unmounts).
 */

/** the system's clock as last read, carried forward by the monotonic clock */
export class LiveClock {
    #base = NaN;
    #at = 0;
    readonly #mono: () => number;

    constructor(mono: () => number = () => performance.now()) {
        this.#mono = mono;
    }

    /** a fresh reading of the system's clock (an ISO time, the API's `now`) */
    anchor(iso: string): void {
        const t = new Date(iso).getTime();
        if (!Number.isFinite(t)) return;
        this.#base = t;
        this.#at = this.#mono();
    }

    /** whether the clock has been read at all */
    get anchored(): boolean {
        return Number.isFinite(this.#base);
    }

    /** the system's time now, in milliseconds since the epoch (NaN before the first reading) */
    now(): number {
        return this.#base + (this.#mono() - this.#at);
    }
}

export interface TickerTimers {
    wall: () => number;
    setTimeout: (f: () => void, ms: number) => unknown;
    clearTimeout: (h: unknown) => void;
    setInterval: (f: () => void, ms: number) => unknown;
    clearInterval: (h: unknown) => void;
}

const browserTimers = (): TickerTimers => ({
    wall: () => Date.now(),
    setTimeout: (f, ms) => setTimeout(f, ms),
    clearTimeout: (h) => clearTimeout(h as ReturnType<typeof setTimeout>),
    setInterval: (f, ms) => setInterval(f, ms),
    clearInterval: (h) => clearInterval(h as ReturnType<typeof setInterval>),
});

/**
 * Calls `tick` at every full second of the browser's clock while running. `run(true)` starts it
 * (ticking once at once, then aligned), `run(false)` pauses it; `stop()` ends it for good.
 */
export class SecondTicker {
    readonly #tick: () => void;
    readonly #t: TickerTimers;
    #align: unknown = null;
    #every: unknown = null;
    #running = false;
    #stopped = false;

    constructor(tick: () => void, timers: TickerTimers = browserTimers()) {
        this.#tick = tick;
        this.#t = timers;
    }

    get running(): boolean {
        return this.#running;
    }

    run(on: boolean): void {
        if (this.#stopped || on === this.#running) return;
        this.#running = on;
        this.#clear();
        if (!on) return;
        this.#tick();
        const toNext = 1000 - (((this.#t.wall() % 1000) + 1000) % 1000);
        this.#align = this.#t.setTimeout(() => {
            this.#align = null;
            this.#tick();
            this.#every = this.#t.setInterval(() => this.#tick(), 1000);
        }, toNext);
    }

    stop(): void {
        this.#stopped = true;
        this.#running = false;
        this.#clear();
    }

    #clear(): void {
        if (this.#align !== null) this.#t.clearTimeout(this.#align);
        if (this.#every !== null) this.#t.clearInterval(this.#every);
        this.#align = this.#every = null;
    }
}

/** how often the page reads the system's time again while the panel is shown */
export const REANCHOR_MS = 60_000;

/**
 * The locale the clocks are written in: the page's language, in the browser's own variant of it
 * when it has one (en-US writes 8:19:33 PM, en-GB 20:19:33), else de-DE or en-GB.
 */
export function clockLocale(language: string, browser: readonly string[] = []): string {
    const own = browser.find((l) => l.toLowerCase().split('-')[0] === language);
    return own ?? (language === 'de' ? 'de-DE' : 'en-GB');
}
