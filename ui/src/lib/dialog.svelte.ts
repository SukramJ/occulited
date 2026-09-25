// The one dialog of the shell (maintainer, 2026-09-09: never window.confirm/prompt/alert - a
// Svelte popup, always). ask(), askText(), askSelect() and askMultiSelect() return promises;
// ConfirmDialog.svelte, mounted once in App.svelte, renders whatever request is pending and
// resolves it. A page that needs a dialog of its own shape (an editor with a form) builds it on
// ModalDialog.svelte instead - the shell's questions still work on top of such a modal.
export interface SelectOption {
    value: string;
    label: string;
    /** task 45: options with the same group are listed under one heading (the taxonomy's name) */
    group?: string;
    /** task 45: 1 for a root node, deeper below - drawn as indentation in the list */
    depth?: number;
}

export interface AskOptions {
    /** the question, one or two sentences - or paragraphs separated by a blank line; the title is optional */
    message: string;
    title?: string;
    /** the confirming button's label; default OK */
    confirm?: string;
    /** the cancelling button's label; default Cancel; '' hides it (a notice with one button) */
    cancel?: string;
    /** a destructive act: the confirming button in the warning colour */
    danger?: boolean;
    /** askText: an input under the message; `initial` is what a rename starts from */
    input?: {label?: string; type?: 'text' | 'password'; placeholder?: string; minLength?: number; initial?: string};
    /**
     * askSelect / askMultiSelect: a list under the message, grouped by `group` and the depth of
     * an entry drawn as indentation (the Metadata editor's "move to" and "assign to"). The list
     * shows up to 20 rows at once (task 45: a tree has to be visible at a glance), bounded by the
     * viewport; `size` overrides that. `multi` makes every row a checkbox and the answer a list.
     */
    select?: {label?: string; options: SelectOption[]; initial?: string; size?: number; multi?: boolean};
    /**
     * ask(): the act itself, run by the confirming button while the dialog stays open with its
     * buttons off. Resolving to nothing closes the dialog as confirmed; resolving to a text keeps it
     * open with that text under the message and only a Close button (the act ran but did not do
     * what was asked); a thrown error is shown the same way and the confirming button stays, for
     * another try.
     */
    run?: () => Promise<string | null | void>;
    /** Cancel gets the focus instead of the confirming button, so Enter does not confirm */
    focusCancel?: boolean;
}

/** What a pending question resolves to: a text or a choice, a list of choices, or null = cancelled. */
export type Answer = string | string[] | null;

interface Pending {
    options: AskOptions;
    resolve: (value: Answer) => void;
}

export const dialog = $state<{pending: Pending | null}>({pending: null});

function open(options: AskOptions): Promise<Answer> {
    // a second question while one is open would steal the first one's answer: refuse it
    if (dialog.pending) dialog.pending.resolve(null);
    return new Promise((resolve) => {
        dialog.pending = {options, resolve};
    });
}

/** A yes/no question; true when confirmed. */
export async function ask(message: string | AskOptions): Promise<boolean> {
    const o = typeof message === 'string' ? {message} : message;
    return (await open(o)) !== null;
}

/** A question with a text input; the text when confirmed, null when cancelled. */
export async function askText(options: AskOptions): Promise<string | null> {
    const v = await open({...options, input: options.input ?? {}});
    return typeof v === 'string' ? v : null;
}

/** A question with a list to pick one entry from; the chosen value when confirmed, null when cancelled. */
export async function askSelect(options: AskOptions & {select: NonNullable<AskOptions['select']>}): Promise<string | null> {
    const v = await open({...options, select: {...options.select, multi: false}});
    return typeof v === 'string' ? v : null;
}

/**
 * task 45: a question with a list to tick any number of entries in; the chosen values in list
 * order when confirmed (never empty: the confirming button is off while nothing is ticked), null
 * when cancelled.
 */
export async function askMultiSelect(options: AskOptions & {select: NonNullable<AskOptions['select']>}): Promise<string[] | null> {
    const v = await open({...options, select: {...options.select, multi: true}});
    return Array.isArray(v) ? v : null;
}

/** Called by the dialog component. */
export function settle(value: Answer): void {
    const p = dialog.pending;
    dialog.pending = null;
    p?.resolve(value);
}
