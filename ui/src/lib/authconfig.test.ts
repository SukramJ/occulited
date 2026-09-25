import {describe, expect, it} from 'vitest';
import {authConfigBody} from './authconfig';

// openccu-lite B-206: the PUT body has exactly the writable keys, whatever GET answered
describe('authConfigBody', () => {
    it('keeps the writable fields and drops the read-only ones', () => {
        const view = {mode: 'oidc' as const, modes: ['local', 'oidc', 'off'], name: 'authentik', issuer: 'https://auth.example.org/application/o/lite/', client_id: 'abc', client_secret_set: true, username_claim: 'preferred_username', scopes: 'openid profile', password_login: true, running: 'local', restart_required: true, something_new: 1};
        const body = authConfigBody(view, '');
        expect(Object.keys(body).sort()).toEqual(['client_id', 'client_secret', 'issuer', 'mode', 'name', 'password_login', 'scopes', 'username_claim']);
        expect(body).toEqual({mode: 'oidc', name: 'authentik', issuer: 'https://auth.example.org/application/o/lite/', client_id: 'abc', client_secret: '', username_claim: 'preferred_username', scopes: 'openid profile', password_login: true});
        expect(authConfigBody(view, 's3cret').client_secret).toBe('s3cret');
    });
});
