import {describe, expect, it} from 'vitest';
import {ageStrings, bech32Encode, emergencyKit, encodeCode, groupCode, kitFileName, normalizeTyped, recoveryKeyFrom, typedKind, x25519} from './recoverykey';

const hex = (s: string) => new Uint8Array(s.match(/../g)!.map((b) => parseInt(b, 16)));

describe('recoverykey', () => {
    it('derives the pinned vector, the same as internal/backupcrypt (TestDerivationVector)', async () => {
        const raw = new Uint8Array(16).map((_, i) => i * 17);
        const k = await recoveryKeyFrom(raw);
        expect(k.code).toBe('0024-H36H-2NCS-VRH6-DAQF-6DVV-QZN3');
        expect(k.recipient).toBe('age14zwu3lsf95tuncdt5qzgp3a7jy7gjdsjmujl7d5n5tdv6qlpfu6qd7fvkk');
        expect(k.identity).toBe('AGE-SECRET-KEY-173SCPSZRZXMZDYVU3QVPS6W8EF4LQWXGD4K06XZJJWMA0MQWA99S0V84MH');
    });

    it('computes X25519 as RFC 7748 §6.1 (Alice)', () => {
        const priv = hex('77076d0a7318a57d3c16c17251b26645df4c2f87ebc0992ab177fba51db92c2a');
        const base = new Uint8Array(32);
        base[0] = 9;
        expect(Buffer.from(x25519(priv, base)).toString('hex')).toBe('8520f0098930a754748b7ddcb43ef75a0dbf3a0d26381af4eba4a98eaa9b4e6a');
        // the shared secret of §6.1
        const bobPub = hex('de9edb7d7b7dc1b4d35b61c2ece435373f8343c85b78674dadfc7e146f882b4f');
        expect(Buffer.from(x25519(priv, bobPub)).toString('hex')).toBe('4a5d9d5ba4ce2de1728e3bf480350f25e07e21c947d19e3376f09b3c1e161742');
    });

    it('encodes Bech32 as BIP-173 does', () => {
        // BIP-173's test vector for the empty data under "a"
        expect(bech32Encode('a', new Uint8Array(0))).toBe('a12uel5l');
        // an age recipient made from a known scalar: the identity round-trips through age's own format
        const scalar = new Uint8Array(32).map((_, i) => i);
        const s = ageStrings(scalar);
        expect(s.identity.startsWith('AGE-SECRET-KEY-1')).toBe(true);
        expect(s.identity).toHaveLength(74);
        expect(s.recipient.startsWith('age1')).toBe(true);
        expect(s.recipient).toHaveLength(62);
    });

    it('groups and forgives typing', async () => {
        const flat = await encodeCode(new Uint8Array(16));
        expect(flat).toHaveLength(28);
        expect(groupCode(flat)).toMatch(/^([0-9A-Z]{4}-){6}[0-9A-Z]{4}$/);
        expect(normalizeTyped(' 0024 h36h-2ncs vrh6\nDAQF-6DVV-QZN3 ')).toBe('0024H36H2NCSVRH6DAQF6DVVQZN3');
        expect(normalizeTyped('oo24-h36h-il')).toBe('0024H36H11');
        expect(typedKind('0024-H36H-2NCS-VRH6-DAQF-6DVV-QZN3')).toBe('code');
        expect(typedKind('age14zwu3lsf95tuncdt5qzgp3a7jy7gjdsjmujl7d5n5tdv6qlpfu6qd7fvkk')).toBe('recipient');
        expect(typedKind('AGE-SECRET-KEY-173SCPSZRZXMZDYVU3QVPS6W8EF4LQWXGD4K06XZJJWMA0MQWA99S0V84MH')).toBe('identity');
        expect(typedKind('age-secret-key-pq-1qq')).toBe('pq');
        expect(typedKind('hello')).toBe('unknown');
        expect(typedKind('0024-H36H-2NCS-VRH6-DAQF-6DVV-QZN')).toBe('unknown');
    });

    it('writes the kit bilingually with the code and the identity once each, the page language first', () => {
        const k = {host: 'lab-ccu', url: 'https://lab-ccu.lan/', created: '2026-09-23', fingerprint: 'ea77-3877-2a7b-31f8', code: '0024-H36H-2NCS-VRH6-DAQF-6DVV-QZN3', identity: 'AGE-SECRET-KEY-173SCPSZRZXMZDYVU3QVPS6W8EF4LQWXGD4K06XZJJWMA0MQWA99S0V84MH', lang: 'en' as const};
        const en = emergencyKit(k);
        expect(en.split(k.code).length).toBe(2);
        expect(en.split(k.identity).length).toBe(2);
        expect(en).toContain('age -d -i key.txt -o backup.sbk backup.sbk.age');
        expect(en).toContain('Backup emergency kit / Notfallkit für Sicherungen');
        expect(en).toContain('lab-ccu  (https://lab-ccu.lan/)');
        expect(en).toContain('ea77-3877-2a7b-31f8');
        expect(en).not.toMatch(/\bbox\b/i);
        const de = emergencyKit({...k, lang: 'de'});
        expect(de).toContain('Notfallkit für Sicherungen / Backup emergency kit');
        expect(de.indexOf('Ohne diesen Schlüssel')).toBeLessThan(de.indexOf('Without this key'));
        // without the identity the PC section is left out
        expect(emergencyKit({...k, identity: ''})).not.toContain('age -d');
        expect(kitFileName('lab ccu/1', '2026-09-23')).toBe('openccu-lite-recovery-key-lab-ccu-1-2026-09-23.txt');
    });
});
