/*
 * openccu-lite task 231: the Trust stores page's pure parts - the four stores' names and order,
 * a distinguished name's common name for a table cell, a short fingerprint, a DER upload turned
 * into the request body, the source column's words and the filter.
 */
import type {Translate} from './warnings';

export type StoreID = 'system' | 'occulited' | 'oidc' | 'acme';

export interface TrustCert {
    id: string;
    purposes?: string[];
    subject: string;
    issuer: string;
    not_before: string;
    not_after: string;
    fingerprint: string;
    ca: boolean;
    self_signed: boolean;
    names?: string[];
    expired?: boolean;
    expires_soon?: boolean;
    added?: string;
    added_by?: string;
    /** image (the bundle's or occulited's base set) or added */
    source?: 'image' | 'added';
    /** where an added one came from: oidc-settings, acme-settings, page, copy */
    origin?: string;
    /** an image certificate the administrator removed: listed, out of the pool, restorable */
    distrusted?: boolean;
    removable?: boolean;
}

export interface TrustStore {
    id: StoreID;
    editable: boolean;
    certificates: TrustCert[];
}

/** a TLS handshake one of occulited's own clients lost to an authority its store lacks */
export interface TrustFailure {
    host: string;
    store: StoreID;
    at: string;
    error: string;
    issuer: string;
    chain?: TrustCert[];
    /** the System store's certificate that would verify the chain: the one-click copy */
    candidate?: TrustCert;
}

export interface TrustView {
    stores: TrustStore[];
    pending: TrustFailure[];
}

export const STORE_ORDER: StoreID[] = ['system', 'occulited', 'oidc', 'acme'];

/** task 267: the store a URL's anchor names (`#oidc`), the System store for anything else */
export function storeFromHash(hash: string): StoreID {
    const id = hash.replace(/^#/, '');
    return (STORE_ORDER as string[]).includes(id) ? (id as StoreID) : 'system';
}

/** the store's heading; 'occulited' and 'ACME' are names, the other two are translated */
export function storeTitle(id: string, t: Translate): string {
    switch (id) {
        case 'system':
            return t('System');
        case 'occulited':
            return 'occulited';
        case 'oidc':
            return t('OAuth / OIDC');
        case 'acme':
            return 'ACME';
    }
    return id;
}

/**
 * The common name of a distinguished name as Go prints it (`CN=x,O=y,C=DE`), else the whole
 * name. A comma inside a value is escaped as `\,` and kept.
 */
export function commonName(dn: string): string {
    const parts = splitDN(dn);
    const cn = parts.find((p) => /^CN=/i.test(p));
    if (cn) return cn.slice(3).trim();
    const o = parts.find((p) => /^O=/i.test(p));
    return o ? o.slice(2).trim() : dn;
}

function splitDN(dn: string): string[] {
    const out: string[] = [];
    let cur = '';
    for (let i = 0; i < dn.length; i++) {
        const ch = dn[i];
        if (ch === '\\' && i + 1 < dn.length) {
            cur += dn[i + 1];
            i++;
        } else if (ch === ',') {
            out.push(cur.trim());
            cur = '';
        } else cur += ch;
    }
    if (cur.trim()) out.push(cur.trim());
    return out;
}

/** the first eight bytes of a colon-separated SHA-256, for the table; the whole one is the title */
export function shortFingerprint(fp: string): string {
    return fp.split(':').slice(0, 8).join(':');
}

/** whether an uploaded file's text is PEM (else it is taken as DER) */
export function isPEM(text: string): boolean {
    return text.includes('-----BEGIN');
}

/** a DER upload as the request's base64 */
export function derBase64(bytes: Uint8Array): string {
    let bin = '';
    for (let i = 0; i < bytes.length; i += 0x8000) bin += String.fromCharCode(...bytes.subarray(i, i + 0x8000));
    return btoa(bin);
}

/** the body POST /trust/{store} takes for an uploaded file: {pem} for PEM text, {der} for DER */
export function uploadBody(bytes: Uint8Array): {pem: string} | {der: string} {
    const text = new TextDecoder('utf-8', {fatal: false}).decode(bytes);
    return isPEM(text) ? {pem: text} : {der: derBase64(bytes)};
}

/** the Source column: the image, or who added it where and when */
export function sourceText(c: TrustCert, t: Translate, day: (iso: string) => string): string {
    if (c.source !== 'added') return t('Image');
    let where = '';
    switch (c.origin) {
        case 'oidc-settings':
            where = t('OIDC settings');
            break;
        case 'acme-settings':
            where = t('ACME settings');
            break;
        case 'copy':
            where = t('copied');
            break;
        default:
            where = t('added');
    }
    const by = c.added_by ? t('by {user}', {user: c.added_by}) : '';
    const when = c.added ? day(c.added) : '';
    return [where, by, when].filter(Boolean).join(' · ');
}

/** the filter: subject, issuer, fingerprint, names and the source words, case-folded */
export function matches(c: TrustCert, q: string, source: string): boolean {
    const s = q.trim().toLowerCase();
    if (!s) return true;
    const fp = c.fingerprint.toLowerCase();
    return [c.subject, c.issuer, source, ...(c.names ?? [])].some((x) => x.toLowerCase().includes(s)) || fp.includes(s) || fp.replaceAll(':', '').includes(s.replaceAll(':', ''));
}

/** the stores a certificate of `from` can be copied into: the others, in the page's order */
export function copyTargets(from: StoreID, stores: TrustStore[]): TrustStore[] {
    return STORE_ORDER.map((id) => stores.find((s) => s.id === id)).filter((s): s is TrustStore => !!s && s.id !== from && s.editable);
}

/** a removal that may break TLS for the whole system or for occulited's own calls asks in red */
export function removalIsDanger(store: StoreID, c: TrustCert): boolean {
    return store === 'system' || (store === 'occulited' && c.source !== 'added');
}
