/**
 * The addon icons and logos an addon declares in its manifest (openccu-lite task 100).
 *
 * The box names them per addon as `images`: kind → the URL on this origin that serves the file -
 * `icon`, `icon-dark`, `logo`, `logo-dark`, the ones the manifest declares and no other
 * (`GET /addons`, `GET /services`, `GET /catalog`). The shell shows them through `<img>` alone; the
 * box answers each with the type its content says, nosniff and a Content-Security-Policy, so an SVG
 * is a picture and nothing more.
 *
 * What is shown where is a list of candidates the `<img>` walks on error (AddonImage.svelte):
 *
 * - the variant for the theme first, the other one as its stand-in (a missing dark variant uses the
 *   light one, a missing light one the dark one);
 * - a logo where an icon is wanted and the other way round, when only one of the two is declared
 *   (manifest-format.md: "if only one of icon and logo is given, the other falls back to it");
 * - then whatever the caller adds behind the manifest's images - the frontend's favicon (task 55),
 *   the logo of the `Info:` line (lib/addonicon.ts) - and the letter when the list is exhausted.
 */

export type ImageKind = 'icon' | 'icon-dark' | 'logo' | 'logo-dark';

export type AddonImages = Partial<Record<ImageKind, string>>;

/** The URL candidates for an icon or a logo of an addon, in the order to try them; [] for none. */
export function imageCandidates(images: AddonImages | undefined, want: 'icon' | 'logo', dark: boolean): string[] {
    if (!images) return [];
    const own: ImageKind[] = want === 'icon' ? ['icon', 'icon-dark'] : ['logo', 'logo-dark'];
    const other: ImageKind[] = want === 'icon' ? ['logo', 'logo-dark'] : ['icon', 'icon-dark'];
    const order = (kinds: ImageKind[]) => (dark ? [kinds[1]!, kinds[0]!] : kinds);
    const out: string[] = [];
    for (const k of [...order(own), ...order(other)]) {
        const u = images[k];
        if (u && isOwnPath(u) && !out.includes(u)) out.push(u);
    }
    return out;
}

/** Only a path of this origin's API is taken from the answer - never a URL to another host. */
function isOwnPath(u: string): boolean {
    return u.startsWith('/api/') && !u.startsWith('//') && !/[<>"'\s]/.test(u);
}
