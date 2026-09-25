/*
 * Task 57: the state of the System menu, shared by the three things that open it - the *System* tab
 * (App.svelte), a system page's title switcher (lib/SystemTitle.svelte) and Ctrl/⌘+K - and the one
 * panel that shows it (lib/SystemMenu.svelte, mounted once in App.svelte). The anchor is what the
 * panel grows out of and shrinks back into, and where the focus returns on Escape.
 *
 * The warning dots read task 81's tracker (GET /api/system/v1/warnings): a page whose active,
 * unsilenced warning leads to it shows one, the tab shows the worst of them. The list is read when
 * the shell signs in, once a minute, and every time the menu opens, so a silence set on the Status
 * page is gone from the menu the next time it is looked at.
 */
import {api} from './api';
import {addonsDot, pageDots, type Severity} from './systemmenu';
import type {WarningsView} from './warnings';

export const systemMenu = $state({
    open: false,
    /** the element the menu grows out of: the System tab, or a page's title switcher */
    anchor: null as HTMLElement | null,
    /**
     * whether the filter takes the focus on open (task 188). Set by `opensFilter` from the click
     * that opened the menu: a keyboard and a mouse or pen focus it, a finger never does - an
     * on-screen keyboard would cover the sheet the finger just opened.
     */
    focusFilter: false,
    /** the System tab, where Ctrl/⌘+K anchors the menu */
    tab: null as HTMLElement | null,
    /** page id → the dot it shows */
    dots: {} as Record<string, Severity>,
    /**
     * task 248: the dot of the Addons tab and of its *Manage addons* entry - the worst active,
     * unsilenced warning that leads to /addons (addon-update, an addon that ended, ...)
     */
    addonDot: undefined as Severity | undefined,
});

export function openSystemMenu(anchor: HTMLElement | null, focusFilter: boolean): void {
    systemMenu.anchor = anchor;
    systemMenu.focusFilter = focusFilter;
    systemMenu.open = true;
}

export function closeSystemMenu(): void {
    systemMenu.open = false;
}

/** the anchor's own click: closes a menu it opened, opens one otherwise (moving it from another anchor) */
export function toggleSystemMenu(anchor: HTMLElement | null, focusFilter: boolean): void {
    if (systemMenu.open && systemMenu.anchor === anchor) closeSystemMenu();
    else openSystemMenu(anchor, focusFilter);
}

export async function refreshDots(): Promise<void> {
    try {
        const r = await api.get<WarningsView>('/api/system/v1/warnings');
        systemMenu.dots = pageDots(r.warnings ?? []);
        systemMenu.addonDot = addonsDot(r.warnings ?? []);
    } catch {
        /* the dots stay as they were; the next read may work */
    }
}
