/*
 * Task 177 (the maintainer: "why does every page render from new when navigating ... cant we do that
 * for all pages?"): the pages stay mounted once visited, as the addon frames do (task 39), and are
 * hidden while another shows. A kept page asks its PageLife whether it is shown: its polls skip while
 * it is not, and it refreshes quietly when it comes back. `search` is the query the page last saw
 * while shown - a hidden page must not read (or write back) another page's query.
 */
import {getContext, setContext} from 'svelte';
import {router} from './router.svelte';

const KEY = Symbol('pagelife');

export class PageLife {
    active = $state(true);
    search = $state('');
    #returns = new Set<() => void>();

    constructor(active: boolean) {
        this.active = active;
        this.search = router.search;
    }

    /** the shell's: shown or hidden; a page coming back runs its onReturn callbacks */
    setActive(v: boolean): void {
        if (v === this.active) return;
        this.active = v;
        if (v) {
            this.search = router.search;
            for (const f of this.#returns) f();
        }
    }

    /** runs f each time the page is shown again after being hidden; the returned function stops it */
    onReturn(f: () => void): () => void {
        this.#returns.add(f);
        return () => this.#returns.delete(f);
    }
}

// a page outside the keeper (a test mounting it alone, the welcome page) is always shown
const ALWAYS = new PageLife(true);

export function providePageLife(l: PageLife): void {
    setContext(KEY, l);
}

/** the page's life; call during component init */
export function pageLife(): PageLife {
    return getContext<PageLife | undefined>(KEY) ?? ALWAYS;
}
