/*
 * The backup recovery key in the browser (openccu-lite task 91, D-64): the code is made here, so
 * the secret never crosses the network at the setup - the system gets the public recipient only.
 *
 * The code: 128 random bits as 26 Crockford base32 symbols (the two leading bits zero) plus two
 * check symbols (the first 10 bits of SHA-256 over the 16 bytes), grouped XXXX-XXXX-… (7 × 4). The
 * age identity is HKDF-SHA256(code, salt, info) as the 32-byte X25519 scalar; the recipient is its
 * public key. Every constant here is mirrored in internal/backupcrypt/code.go, and both sides pin the
 * same vector in their tests: a change on one side makes every printed recovery key useless.
 *
 * WebCrypto (SHA-256, HKDF) exists only in a secure context: on plain HTTP the page asks the system
 * to generate the key instead (canGenerateLocally()). X25519 and Bech32 are plain arithmetic here.
 */

const ALPHABET = '0123456789ABCDEFGHJKMNPQRSTVWXYZ';
const HKDF_SALT = 'openccu-lite backup recovery key';
const HKDF_INFO = 'age X25519 identity v1';

/** whether the browser can make the key itself (WebCrypto needs a secure context) */
export function canGenerateLocally(): boolean {
    return typeof crypto !== 'undefined' && !!crypto.subtle && typeof crypto.getRandomValues === 'function';
}

/** 16 random bytes into the 28 ungrouped symbols */
export async function encodeCode(raw: Uint8Array<ArrayBuffer>): Promise<string> {
    if (raw.length !== 16) throw new Error('16 bytes');
    let out = '';
    let acc = 0;
    let bits = 2; // two zero bits in front: 130 bits, 26 symbols
    for (const b of raw) {
        acc = ((acc << 8) | b) >>> 0;
        bits += 8;
        while (bits >= 5) {
            bits -= 5;
            out += ALPHABET[(acc >>> bits) & 31];
        }
    }
    const sum = new Uint8Array(await crypto.subtle.digest('SHA-256', raw));
    const check = (((sum[0] ?? 0) << 2) | ((sum[1] ?? 0) >> 6)) & 1023;
    out += (ALPHABET[check >> 5] ?? '') + (ALPHABET[check & 31] ?? '');
    return out;
}

/** XXXX-XXXX-… for reading */
export function groupCode(flat: string): string {
    return flat.replace(/-/g, '').match(/.{1,4}/g)?.join('-') ?? flat;
}

/**
 * What the user typed, normalised the way the system reads it: upper case, spaces and hyphens gone,
 * i/l read as 1 and o as 0. Only the shape is judged here - the checksum is the system's answer.
 */
export function normalizeTyped(s: string): string {
    return s.replace(/[\s_-]+/g, '').toUpperCase().replace(/[IL]/g, '1').replace(/O/g, '0');
}

/** the kind of thing typed: a code, an age identity, an age1 recipient (the wrong half), or unknown */
export function typedKind(s: string): 'code' | 'identity' | 'recipient' | 'pq' | 'unknown' {
    const flat = s.replace(/\s+/g, '').toUpperCase();
    if (flat.startsWith('AGE1')) return 'recipient';
    if (flat.startsWith('AGE-SECRET-KEY-PQ-') || flat.startsWith('AGE-PLUGIN-')) return 'pq';
    if (flat.startsWith('AGE-SECRET-KEY-1')) return 'identity';
    const n = normalizeTyped(s);
    return n.length === 28 && /^[0-9A-HJKMNP-TV-Z]+$/.test(n) ? 'code' : 'unknown';
}

// --- X25519 (RFC 7748) over BigInt: the recipient from the derived scalar ---

const P = (1n << 255n) - 19n;
const A24 = 121665n;

function mod(a: bigint): bigint {
    const r = a % P;
    return r < 0n ? r + P : r;
}

function powMod(b: bigint, e: bigint): bigint {
    let r = 1n;
    b = mod(b);
    while (e > 0n) {
        if (e & 1n) r = mod(r * b);
        b = mod(b * b);
        e >>= 1n;
    }
    return r;
}

function decodeLE(b: Uint8Array): bigint {
    let n = 0n;
    for (let i = b.length - 1; i >= 0; i--) n = (n << 8n) | BigInt(b[i] ?? 0);
    return n;
}

function encodeLE(n: bigint): Uint8Array {
    const out = new Uint8Array(32);
    for (let i = 0; i < 32; i++) {
        out[i] = Number(n & 255n);
        n >>= 8n;
    }
    return out;
}

/** X25519(k, u): the scalar clamped as the RFC says, u masked to 255 bits */
export function x25519(scalar: Uint8Array, u: Uint8Array): Uint8Array {
    const k = new Uint8Array(scalar);
    k[0] = (k[0] ?? 0) & 248;
    k[31] = ((k[31] ?? 0) & 127) | 64;
    const kn = decodeLE(k);
    const x1 = decodeLE(u) & ((1n << 255n) - 1n);
    let x2 = 1n, z2 = 0n, x3 = x1, z3 = 1n, swap = 0n;
    for (let t = 254; t >= 0; t--) {
        const kt = (kn >> BigInt(t)) & 1n;
        swap ^= kt;
        if (swap) {
            [x2, x3] = [x3, x2];
            [z2, z3] = [z3, z2];
        }
        swap = kt;
        const a = mod(x2 + z2), aa = mod(a * a);
        const b = mod(x2 - z2), bb = mod(b * b);
        const e = mod(aa - bb);
        const c = mod(x3 + z3);
        const d = mod(x3 - z3);
        const da = mod(d * a), cb = mod(c * b);
        x3 = mod((da + cb) ** 2n);
        z3 = mod(x1 * (da - cb) ** 2n);
        x2 = mod(aa * bb);
        z2 = mod(e * (aa + A24 * e));
    }
    if (swap) {
        [x2, x3] = [x3, x2];
        [z2, z3] = [z3, z2];
    }
    return encodeLE(mod(x2 * powMod(z2, P - 2n)));
}

const BASEPOINT = (() => {
    const b = new Uint8Array(32);
    b[0] = 9;
    return b;
})();

// --- Bech32 (BIP-173), encoding only ---

const BECH32 = 'qpzry9x8gf2tvdw0s3jn54khce6mua7l';
const GEN = [0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3];

function polymod(values: number[]): number {
    let chk = 1;
    for (const v of values) {
        const top = chk >>> 25;
        chk = (((chk & 0x1ffffff) << 5) ^ v) >>> 0;
        for (let i = 0; i < 5; i++) if ((top >>> i) & 1) chk = (chk ^ (GEN[i] ?? 0)) >>> 0;
    }
    return chk;
}

/** the Bech32 string for data under the lower-case hrp */
export function bech32Encode(hrp: string, data: Uint8Array): string {
    const five: number[] = [];
    let acc = 0, bits = 0;
    for (const b of data) {
        acc = ((acc << 8) | b) >>> 0;
        bits += 8;
        while (bits >= 5) {
            bits -= 5;
            five.push((acc >>> bits) & 31);
        }
    }
    if (bits > 0) five.push((acc << (5 - bits)) & 31);
    const expand = [...hrp].map((c) => c.charCodeAt(0) >> 5).concat([0], [...hrp].map((c) => c.charCodeAt(0) & 31));
    const pm = polymod([...expand, ...five, 0, 0, 0, 0, 0, 0]) ^ 1;
    let out = hrp + '1' + five.map((v) => BECH32[v]).join('');
    for (let i = 0; i < 6; i++) out += BECH32[(pm >>> (5 * (5 - i))) & 31];
    return out;
}

/** the age identity (AGE-SECRET-KEY-1…) and recipient (age1…) of a 32-byte scalar */
export function ageStrings(scalar: Uint8Array): {identity: string; recipient: string} {
    return {
        identity: bech32Encode('age-secret-key-', scalar).toUpperCase(),
        recipient: bech32Encode('age', x25519(scalar, BASEPOINT)),
    };
}

/** HKDF-SHA256 of the 16 code bytes: the X25519 scalar */
export async function deriveScalar(raw: Uint8Array<ArrayBuffer>): Promise<Uint8Array> {
    const key = await crypto.subtle.importKey('raw', raw, 'HKDF', false, ['deriveBits']);
    const enc = new TextEncoder();
    const bits = await crypto.subtle.deriveBits({name: 'HKDF', hash: 'SHA-256', salt: enc.encode(HKDF_SALT), info: enc.encode(HKDF_INFO)}, key, 256);
    return new Uint8Array(bits);
}

export interface RecoveryKey {
    /** grouped, as shown and printed */
    code: string;
    recipient: string;
    identity: string;
}

/** the whole derivation for 16 given bytes (the tests' vector) */
export async function recoveryKeyFrom(raw: Uint8Array<ArrayBuffer>): Promise<RecoveryKey> {
    const code = groupCode(await encodeCode(raw));
    const {identity, recipient} = ageStrings(await deriveScalar(raw));
    return {code, recipient, identity};
}

/** a fresh recovery key, made in this browser */
export async function generateRecoveryKey(): Promise<RecoveryKey> {
    const raw = new Uint8Array(16);
    crypto.getRandomValues(raw);
    return recoveryKeyFrom(raw);
}

// --- the emergency kit ---

export interface KitInput {
    host: string;
    url: string;
    /** YYYY-MM-DD */
    created: string;
    fingerprint: string;
    code: string;
    /** the age identity for age -d on a PC; '' leaves that section out */
    identity: string;
    lang: 'de' | 'en';
}

/** the kit's file name */
export function kitFileName(host: string, created: string): string {
    return `openccu-lite-recovery-key-${host.replace(/[^A-Za-z0-9._-]+/g, '-')}-${created}.txt`;
}

/**
 * The emergency kit, bilingual in one file - the page's language first on every line pair, since
 * it may be read years later by someone else. Text only: what a password manager's note or a
 * printout keeps. The QR code of the print view is the code itself.
 */
export function emergencyKit(k: KitInput): string {
    const pair = (de: string, en: string) => (k.lang === 'de' ? [de, en] : [en, de]);
    const both = (de: string, en: string, sep = ' / ') => pair(de, en).join(sep);
    const L: string[] = [];
    L.push(`openccu-lite – ${both('Notfallkit für Sicherungen', 'Backup emergency kit')}`);
    L.push('');
    L.push(`System:                     ${k.host}${k.url ? `  (${k.url})` : ''}`);
    L.push(`${both('Erstellt', 'Created')}:          ${k.created}`);
    L.push(`${both('Schlüssel-Fingerabdruck', 'Key fingerprint')}:  ${k.fingerprint}`);
    L.push('');
    L.push(`${both('Wiederherstellungsschlüssel', 'Recovery key')}:`);
    L.push(`  ${k.code}`);
    L.push('');
    L.push(`${both('Wiederherstellen auf openccu-lite', 'Restore on openccu-lite')}:`);
    L.push(...pair('  System → Sicherung → Sicherung wiederherstellen → die .sbk.age wählen → diesen Schlüssel eingeben.', '  System → Backup → Restore a backup → choose the .sbk.age → enter this key.'));
    if (k.identity) {
        L.push('');
        L.push(`${both('Ohne openccu-lite (OpenCCU, CCU3, ein PC)', 'Without openccu-lite (OpenCCU, a CCU3, a PC)')}:`);
        L.push(...pair('  Dem Schlüssel entspricht diese age-Identität:', '  The key is this age identity:'));
        L.push(`  ${k.identity}`);
        L.push(`  1. ${both('age installieren', 'install age')}: https://age-encryption.org`);
        L.push(`  2. ${both('Die AGE-SECRET-KEY-Zeile als key.txt speichern', 'Save the AGE-SECRET-KEY line as key.txt')}`);
        L.push('  3. age -d -i key.txt -o backup.sbk backup.sbk.age');
        L.push(`  4. ${both('backup.sbk wie gewohnt wiederherstellen', 'restore backup.sbk as usual')}`);
    }
    L.push('');
    L.push(...pair('Ohne diesen Schlüssel kann niemand diese Sicherungen wiederherstellen – weder openccu-lite noch das Forum.', 'Without this key nobody can restore these backups - not openccu-lite, not the forum.'));
    L.push(...pair('Nicht auf dem System und nicht neben den Sicherungen aufbewahren.', 'Do not keep it on the system or next to the backups.'));
    L.push('');
    return L.join('\n');
}

/** today as YYYY-MM-DD in local time */
export function isoDate(d = new Date()): string {
    const p = (n: number) => String(n).padStart(2, '0');
    return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
}
