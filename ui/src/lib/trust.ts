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
    /** the SHA-256 of the public key, base64 (task 232: what a pin matches) */
    spki: string;
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

/** openccu-lite task 232: a pinned public key of the OIDC or ACME store */
export interface Pin {
    id: string;
    purpose: PinPurpose;
    /** the SHA-256 of the public key, base64 */
    spki: string;
    /** with-ca: the CA check as well (the default); only: the pin alone vouches */
    mode: PinMode;
    /** the certificate the pin was taken from; absent for a bare hash (a backup pin) */
    subject?: string;
    not_after?: string;
    fingerprint?: string;
    added: string;
    added_by?: string;
}
export type PinMode = 'with-ca' | 'only';
export type PinPurpose = 'oidc' | 'acme';

/** a connection whose chain matched none of its purpose's pins */
export interface PinFailure {
    purpose: PinPurpose;
    host: string;
    at: string;
    error: string;
    /** what the server presented, the leaf first */
    chain?: TrustCert[];
}

/** one certificate of the chain the purpose's server presents (POST /trust/{store}/pins/peer) */
export interface PeerCert extends TrustCert {
    pem: string;
    /** its key is one of the purpose's pins already */
    pinned: boolean;
}
export interface PeerAnswer {
    url: string;
    host: string;
    chain: PeerCert[];
    verified: boolean;
    error?: string;
    pin_only?: boolean;
}

export interface TrustStore {
    id: StoreID;
    editable: boolean;
    certificates: TrustCert[];
    /** only the oidc and acme stores have pins */
    pins?: Pin[];
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
    pin_failures?: PinFailure[];
}

/** the stores that take pins (the maintainer, 2026-10-03: OIDC and ACME, nothing else) */
export function isPinPurpose(id: string): id is PinPurpose {
    return id === 'oidc' || id === 'acme';
}

/** the mode's words: what the pin is checked besides */
export function pinModeText(mode: PinMode, t: Translate): string {
    return mode === 'only' ? t('pin only') : t('with the CA check');
}

/** the first twelve characters of a base64 key hash, for a table; the whole one is the title */
export function shortSPKI(spki: string): string {
    return spki.length > 14 ? spki.slice(0, 12) + '…' : spki;
}

/** the pin's name in a list: the certificate's common name, or the bare hash */
export function pinName(p: Pin, t: Translate): string {
    return p.subject ? commonName(p.subject) : t('Key hash (backup pin)');
}

/** the leaf of a fetched chain: the first certificate, the one Pin the current certificate takes */
export function leafOf(chain: PeerCert[]): PeerCert | undefined {
    return chain[0];
}

/** whether a typed key hash has the shape the server accepts: 44 base64 characters or 64 hex digits */
export function looksLikeSPKI(s: string): boolean {
    const v = s.trim().replace(/^sha256\/\//, '');
    if (/^[A-Za-z0-9+/]{43}=$/.test(v)) return true;
    return /^[0-9A-Fa-f]{64}$/.test(v.replace(/[:\s-]/g, ''));
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
