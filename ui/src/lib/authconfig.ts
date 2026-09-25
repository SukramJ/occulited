/*
 * openccu-lite B-206 (the maintainer: "when trying to save authentik config i get json unknown
 * field modes"): PUT /api/auth/v1/config takes the writable fields only - GET's answer carries
 * read-only ones too (modes, client_secret_set, restart_required, running), and the API refuses
 * any field it does not know. The body is built here, key by key, never from the view.
 */

export interface AuthConfigPut {
    mode: 'local' | 'oidc' | 'off';
    name: string;
    issuer: string;
    client_id: string;
    /** write-only: empty keeps the stored secret */
    client_secret: string;
    username_claim: string;
    scopes: string;
    password_login: boolean;
}

export function authConfigBody(v: Omit<AuthConfigPut, 'client_secret'>, secret: string): AuthConfigPut {
    return {
        mode: v.mode,
        name: v.name,
        issuer: v.issuer,
        client_id: v.client_id,
        client_secret: secret,
        username_claim: v.username_claim,
        scopes: v.scopes,
        password_login: v.password_login,
    };
}
