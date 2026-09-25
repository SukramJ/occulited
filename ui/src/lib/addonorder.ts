/**
 * Task 59: the first group of the addon dropdown - the addons with a web frontend of their own -
 * in the user's order, and which of them are pinned to the tab bar. The user's choice is stored
 * per account on the box (lib/prefs.svelte.ts, D-54); what is shown is that list reconciled
 * against the addons that are actually there, which is what these functions do. Pure, so they
 * are tested without a browser. The drag and the keys that change the order are lib/sortable.ts
 * (task 162), shared with every other list ordered by hand.
 */
export interface AddonPref {
    id: string;
    pinned: boolean;
}

/**
 * The first group as the dropdown shows it: the stored order for the addons that are there, then
 * the addons the stored list does not name - new ones - in the order given (by name), unpinned.
 * An entry whose addon is gone, switched off or without a frontend is skipped; it stays in the
 * stored list until the next write, so an addon that comes back keeps its place.
 */
export function arrange(ids: readonly string[], stored: readonly AddonPref[]): AddonPref[] {
    const present = new Set(ids);
    const seen = new Set<string>();
    const out: AddonPref[] = [];
    for (const p of stored) {
        if (!present.has(p.id) || seen.has(p.id)) continue;
        seen.add(p.id);
        out.push({id: p.id, pinned: p.pinned});
    }
    for (const id of ids) {
        if (seen.has(id)) continue;
        seen.add(id);
        out.push({id, pinned: false});
    }
    return out;
}

/** The list with one addon's pin set. */
export function withPin(list: readonly AddonPref[], id: string, pinned: boolean): AddonPref[] {
    return list.map((p) => (p.id === id ? {id, pinned} : {...p}));
}

/** Whether two lists say the same, entry by entry. */
export function same(a: readonly AddonPref[], b: readonly AddonPref[]): boolean {
    return a.length === b.length && a.every((p, i) => p.id === b[i]!.id && p.pinned === b[i]!.pinned);
}

/**
 * Below this window width there is no fold: 700 px is where app.css makes the bar two rows and the
 * nav its own line, which scrolls sideways (task 191), so every pinned tab is shown and none has to
 * fit. Until task 191 every pinned tab folded into the Addons dropdown there instead, because a
 * wrapping bar would have grown by a row.
 *
 * Above it the fold is by measurement, live (AddonMenu.svelte `fit`): as many pinned tabs, from
 * the start of the order, as fit on the bar's one row beside the fixed tabs; the rest fold. A
 * fixed breakpoint could not do it - measured on 2026-09-15 at 1280 px against the stub, the
 * fixed tabs took 780 px in German (677 px in English) of the 853 px the nav can have, so there
 * were 73 px (176 px) left, and three pinned addons (RedMatic 100, ioBroker 94, Homematic-Manager
 * 177) wanted 377 px: the breakpoint would have been ~1580 px, above every 1280 px desktop. Most of
 * the fixed width was the System button's reserved breadcrumb (200 px, a plain tab after task 57)
 * and the stub's `CCU WebUI ↗` nav.d entry (109 px); the live fit gives that room to the pins as
 * soon as it exists.
 *
 * Re-measured on 2026-09-16 after task 131 (D-86) took Interfaces and Metadata out of the bar: the
 * room is the window's width minus 451 px, of which the fixed tabs (Status, Addons, System and the
 * stub's nav.d entry) take 343 px in English and 394 px in German (*Zusatzsoftware*). At 1280 px
 * that leaves 486 px (435 px), and the three pinned addons of the stub (Homematic-Manager 157,
 * ioBroker 74, RedMatic 100, 337 px with the gaps) all stand in the bar; in English the first one
 * folds below about 953 px, in German below about 1004 px.
 *
 * Again after the maintainer's follow-up (2026-09-16) moved the nav.d entry into the dropdown: the
 * fixed tabs (Status, Addons, System) take 232 px in English and 284 px in German, so at 1280 px
 * 597 px (546 px) are left, and the first of the three pinned stub addons folds below about 842 px
 * in English and 894 px in German.
 */
export const PHONE_BELOW_PX = 701;
