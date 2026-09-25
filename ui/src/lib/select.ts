// task 40: the option list of Select.svelte, ported from homematic-manager's
// packages/ui/src/lib/components/multiSelect.ts (one kit, D-21) - the single-value half only.

/** One entry of {@link Select}. */
export interface SelectOption {
    readonly value: string;
    readonly label: string;
    readonly disabled?: boolean;
}

/** The entries whose label or value contains the filter text, case-insensitively: an entry shown
 *  by a name (the Log page's "HmIP server") is still found by what it stands for (hmipserver). */
export function filterOptions(options: readonly SelectOption[], filter: string): SelectOption[] {
    const needle = filter.trim().toLowerCase();
    if (needle === '') {
        return [...options];
    }
    return options.filter((option) => option.label.toLowerCase().includes(needle) || option.value.toLowerCase().includes(needle));
}

/** The index the highlight moves to: `delta` steps from `from`, skipping disabled entries,
 *  stopping at the ends rather than wrapping (a list of forty units is read top to bottom). */
export function step(options: readonly SelectOption[], from: number, delta: number): number {
    for (let i = from + delta; i >= 0 && i < options.length; i += delta) {
        if (options[i]?.disabled !== true) {
            return i;
        }
    }
    return from;
}
