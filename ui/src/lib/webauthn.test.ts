import {describe, expect, it} from 'vitest';
import {b64url, bareName, ceremonyError, creationOptions, isSecondFactor, onAddress, requestOptions} from './webauthn';

// openccu-lite task 262: the conversions between the API's JSON and the browser's structures.
describe('webauthn helpers', () => {
    it('base64url round-trips without padding', () => {
        const bytes = new Uint8Array([0, 1, 2, 250, 251, 252, 253, 254, 255]).buffer;
        const s = b64url.encode(bytes);
        expect(s).not.toMatch(/[+/=]/);
        expect(new Uint8Array(b64url.decode(s))).toEqual(new Uint8Array(bytes));
        expect(new Uint8Array(b64url.decode('AQID'))).toEqual(new Uint8Array([1, 2, 3]));
    });
    it('decodes the creation and request options', () => {
        const c = creationOptions({challenge: 'AQID', rp: {id: 'ccu.lan', name: 'openccu-lite'}, user: {id: 'YWJj', name: 'admin', displayName: 'admin'}, pubKeyCredParams: [{type: 'public-key', alg: -7}], excludeCredentials: [{id: 'AQID', type: 'public-key', transports: ['usb']}]});
        expect(new Uint8Array(c.challenge as ArrayBuffer)).toEqual(new Uint8Array([1, 2, 3]));
        expect(new TextDecoder().decode(c.user.id as ArrayBuffer)).toBe('abc');
        expect(c.excludeCredentials?.[0]?.type).toBe('public-key');
        const r = requestOptions({challenge: 'AQID', rpId: 'ccu.lan', allowCredentials: [{id: 'AQID', type: 'public-key'}], userVerification: 'required'});
        expect(r.allowCredentials).toHaveLength(1);
        expect(r.userVerification).toBe('required');
        expect(requestOptions({challenge: 'AQID'}).allowCredentials).toEqual([]);
    });
    it('knows an address and a bare name from the full name', () => {
        expect(onAddress('192.0.2.10')).toBe(true);
        expect(onAddress('[2001:db8::1]')).toBe(true);
        expect(onAddress('ccu.lan')).toBe(false);
        expect(bareName('ccu')).toBe(true);
        expect(bareName('localhost')).toBe(false);
        expect(bareName('ccu.lan')).toBe(false);
        expect(bareName('192.0.2.10')).toBe(false);
    });
    it('tells the login answers apart and names the browser errors', () => {
        expect(isSecondFactor({second_factor: 'webauthn', login: 'x', options: {challenge: 'AQID'}})).toBe(true);
        expect(isSecondFactor({sid: 'ABC'})).toBe(false);
        expect(ceremonyError({name: 'NotAllowedError'})).toBe('not-allowed');
        expect(ceremonyError({name: 'SecurityError'})).toBe('security');
        expect(ceremonyError({name: 'AbortError'})).toBe('cancelled');
        expect(ceremonyError(new Error('x'))).toBe('other');
    });
});
