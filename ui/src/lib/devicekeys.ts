// Task 154: the HmIP device keys on the page - the code a sticker's QR holds, the SGTIN as printed,
// and the printed 26-character KEY for the key sheet. Task 89: a Wi-Fi network's QR code (WIFI:),
// read by the same scanner.

export const DEVICE_KEYS = '/api/system/v1/radio/hmip/device-keys';

/** One line of the section: a paired HmIP device, or a stored key that matches none. No key. */
export interface DeviceKeyRow {
    sgtin?: string;
    address?: string;
    type?: string;
    name?: string;
    paired: boolean;
    has_key: boolean;
    pending?: boolean;
}

export interface DeviceKeysView {
    rows: DeviceKeyRow[];
    paired: number;
    with_key: number;
    stored: number;
    pending: number;
    devices_known: boolean;
    unread?: string;
    bad_lines?: number;
    applying?: boolean;
    error?: string;
}

export interface AddResult {
    sgtin: string;
    address: string;
    paired: boolean;
    replaced?: boolean;
    same?: boolean;
}

export interface ExportedKey {
    sgtin: string;
    key: string;
    payload: string;
    address?: string;
    type?: string;
    name?: string;
}

const squeeze = (s: string) => s.replace(/[\s-]/g, '').toUpperCase();

/** A sticker's code, EQ01SG<SGTIN>DLK<key>, anywhere in the text; null when there is none. */
export function parseDeviceCode(text: string): {sgtin: string; key: string} | null {
    const m = /EQ01SG([0-9A-F]{24})DLK([0-9A-F]{32})/.exec(squeeze(text));
    return m?.[1] && m[2] ? {sgtin: m[1], key: m[2]} : null;
}

/** A typed SGTIN, dashes and case as they come; null when it is not 24 hex digits. */
export function normalizeSGTIN(s: string): string | null {
    const v = squeeze(s);
    return /^[0-9A-F]{24}$/.test(v) ? v : null;
}

/** The SGTIN in groups of four, as the sticker prints it. */
export const formatSGTIN = (s: string) => s.match(/.{1,4}/g)?.join('-') ?? s;

/** The device address hmipserver lists for an SGTIN: its last 14 digits. */
export const addressOf = (sgtin: string) => sgtin.slice(10);

const PRINTED = '0123456789ABCEFGHJKLMNPQRSTUWXYZ';

/** The key as the sticker prints it: 26 characters of eQ-3's alphabet in groups of 5-5-5-5-6. */
export function printedKey(hex: string): string {
    let n = BigInt('0x' + hex);
    let out = '';
    for (let i = 0; i < 26; i++) {
        out = PRINTED[Number(n % 32n)] + out;
        n /= 32n;
    }
    return [out.slice(0, 5), out.slice(5, 10), out.slice(10, 15), out.slice(15, 20), out.slice(20)].join('-');
}

/** Is the typed key the printed 26 characters or 32 hex digits (what the system accepts)? */
export function keyShape(s: string): 'printed' | 'hex' | 'odiv' | '' {
    const v = squeeze(s);
    if (/^[0-9A-F]{32}$/.test(v)) return 'hex';
    if (v.length === 26 && /^[0-9A-Z]+$/.test(v)) return /[DIOV]/.test(v) ? 'odiv' : 'printed';
    return '';
}

export interface WifiCode {
    ssid: string;
    password: string;
    /** the code's T: WPA (WPA/WPA2/WPA3 personal), SAE, WEP, nopass */
    type: string;
    hidden: boolean;
}

/** A Wi-Fi network's QR code (WIFI:T:WPA;S:name;P:secret;H:true;;), backslash escapes undone; null otherwise. */
export function parseWifiCode(text: string): WifiCode | null {
    if (!/^WIFI:/i.test(text.trim())) return null;
    const body = text.trim().slice(5);
    const fields: Record<string, string> = {};
    let key = '';
    let val = '';
    let inKey = true;
    for (let i = 0; i < body.length; i++) {
        const c = body[i];
        if (c === '\\' && i + 1 < body.length) {
            if (inKey) key += body[++i];
            else val += body[++i];
            continue;
        }
        if (inKey && c === ':') {
            inKey = false;
            continue;
        }
        if (c === ';') {
            if (key) fields[key.toUpperCase()] = val;
            key = val = '';
            inKey = true;
            continue;
        }
        if (inKey) key += c;
        else val += c;
    }
    if (key && !inKey) fields[key.toUpperCase()] = val;
    if (!fields.S) return null;
    return {ssid: fields.S, password: fields.P ?? '', type: (fields.T ?? 'nopass').toUpperCase(), hidden: /^true$/i.test(fields.H ?? '')};
}
