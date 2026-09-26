import {describe, expect, it} from 'vitest';
import {commonName, copyTargets, derBase64, isPEM, matches, removalIsDanger, shortFingerprint, sourceText, storeFromHash, storeTitle, uploadBody, type TrustCert, type TrustStore} from './trust';

const t = (key: string, params?: Record<string, string | number>) => {
    let s = key;
    for (const [k, v] of Object.entries(params ?? {})) s = s.replaceAll(`{${k}}`, String(v));
    return s;
};
const cert = (o: Partial<TrustCert> = {}): TrustCert => ({id: 'a', subject: 'CN=ISRG Root X1,O=Internet Security Research Group,C=US', issuer: 'CN=ISRG Root X1,O=Internet Security Research Group,C=US', not_before: '2015-06-04T11:04:38Z', not_after: '2035-06-04T11:04:38Z', fingerprint: '96:BC:EC:06:26:49:76:F3:74:60:77:9A:CF:28:C5:A7:CF:E8:A3:C0:AA:E1:1A:8F:FC:EE:05:C0:BD:DF:08:C6', ca: true, self_signed: true, ...o});

describe('the Trust stores page (openccu-lite task 231)', () => {
    it('names the four stores', () => {
        expect(['system', 'occulited', 'oidc', 'acme'].map((id) => storeTitle(id, t))).toEqual(['System', 'occulited', 'OAuth / OIDC', 'ACME']);
        expect(storeTitle('other', t)).toBe('other');
    });

    it('takes the common name out of a distinguished name, escapes kept', () => {
        expect(commonName('CN=ISRG Root X1,O=Internet Security Research Group,C=US')).toBe('ISRG Root X1');
        expect(commonName('C=US,O=DigiCert Inc,OU=www.digicert.com,CN=DigiCert Global Root G2')).toBe('DigiCert Global Root G2');
        expect(commonName('CN=Lab\\, Inc CA,O=Lab')).toBe('Lab, Inc CA');
        expect(commonName('O=Only an organisation,C=DE')).toBe('Only an organisation');
        expect(commonName('serialNumber=1')).toBe('serialNumber=1');
    });

    it('shortens a fingerprint to its first eight bytes', () => {
        expect(shortFingerprint(cert().fingerprint)).toBe('96:BC:EC:06:26:49:76:F3');
    });

    it('tells PEM from DER and encodes a DER upload', () => {
        expect(isPEM('-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n')).toBe(true);
        expect(isPEM('hello')).toBe(false);
        expect(derBase64(new Uint8Array([0x30, 0x82, 0x01, 0x00]))).toBe('MIIBAA==');
        expect(uploadBody(new TextEncoder().encode('-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n'))).toEqual({pem: '-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n'});
        expect(uploadBody(new Uint8Array([0x30, 0x82, 0x01, 0x00]))).toEqual({der: 'MIIBAA=='});
        // a big DER does not overflow the call stack
        expect(derBase64(new Uint8Array(70000)).length).toBeGreaterThan(90000);
    });

    it('words the source: the image, or who added it where', () => {
        const day = (iso: string) => iso.slice(0, 10);
        expect(sourceText(cert({source: 'image'}), t, day)).toBe('Image');
        expect(sourceText(cert(), t, day)).toBe('Image');
        expect(sourceText(cert({source: 'added', origin: 'page', added_by: 'admin', added: '2026-09-25T10:00:00Z'}), t, day)).toBe('added · by admin · 2026-09-25');
        expect(sourceText(cert({source: 'added', origin: 'oidc-settings', added_by: 'admin'}), t, day)).toBe('OIDC settings · by admin');
        expect(sourceText(cert({source: 'added', origin: 'acme-settings'}), t, day)).toBe('ACME settings');
        expect(sourceText(cert({source: 'added', origin: 'copy', added: '2026-09-25T10:00:00Z'}), t, day)).toBe('copied · 2026-09-25');
        expect(sourceText(cert({source: 'added'}), t, day)).toBe('added');
    });

    it('filters by subject, issuer, fingerprint (with or without colons), names and source', () => {
        const c = cert({names: ['auth.example.org']});
        expect(matches(c, '', 'Image')).toBe(true);
        expect(matches(c, 'isrg', 'Image')).toBe(true);
        expect(matches(c, '96:bc:ec', 'Image')).toBe(true);
        expect(matches(c, '96bcec06', 'Image')).toBe(true);
        expect(matches(c, 'auth.example', 'Image')).toBe(true);
        expect(matches(c, 'image', 'Image')).toBe(true);
        expect(matches(c, 'digicert', 'Image')).toBe(false);
    });

    it('offers the other editable stores as copy targets, in the page order', () => {
        const stores: TrustStore[] = [
            {id: 'acme', editable: true, certificates: []},
            {id: 'system', editable: false, certificates: []},
            {id: 'oidc', editable: true, certificates: []},
            {id: 'occulited', editable: true, certificates: []},
        ];
        expect(copyTargets('system', stores).map((s) => s.id)).toEqual(['occulited', 'oidc', 'acme']);
        expect(copyTargets('oidc', stores).map((s) => s.id)).toEqual(['occulited', 'acme']);
    });

    it('asks in red for the system store and for occulited\'s base set only', () => {
        expect(removalIsDanger('system', cert({source: 'added'}))).toBe(true);
        expect(removalIsDanger('occulited', cert({source: 'image'}))).toBe(true);
        expect(removalIsDanger('occulited', cert({source: 'added'}))).toBe(false);
        expect(removalIsDanger('oidc', cert({source: 'added'}))).toBe(false);
        expect(removalIsDanger('acme', cert({source: 'added'}))).toBe(false);
    });
});

// task 267: the URL's anchor chooses the store's tab
describe('storeFromHash', () => {
    it('names a store by its anchor, the System store for anything else', () => {
        expect(storeFromHash('#oidc')).toBe('oidc');
        expect(storeFromHash('#acme')).toBe('acme');
        expect(storeFromHash('#occulited')).toBe('occulited');
        expect(storeFromHash('')).toBe('system');
        expect(storeFromHash('#nope')).toBe('system');
        expect(storeFromHash('#OIDC')).toBe('system');
    });
});
