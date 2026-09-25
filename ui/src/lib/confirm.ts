// A confirmed ticket (task 154, D-104; task 185): an action that asks who the user is every time,
// even in a valid session. The account's password when it has one; a fresh login at the identity
// provider when it signs in there - the page leaves then, and comes back with the ticket in the
// fragment (#confirm=… or #confirm-error=…), which returned() reads.
import {api, ApiError} from './api';
import {ask, askText} from './dialog.svelte';
import {t} from './i18n.svelte';

export interface ConfirmTexts {
    title: string;
    /** why the password is asked, under the password field */
    message: string;
    /** why the page goes to the identity provider, before it goes */
    provider: string;
    /** the account can confirm neither way */
    impossible: string;
}

/**
 * A ticket for path, or null when the user cancelled or the page is on its way to the identity
 * provider - beforeLeave runs just before it goes, to keep what the page resumes with. An error is
 * thrown with the reason the page shows.
 */
export async function confirmTicket(path: string, texts: ConfirmTexts, beforeLeave?: () => void): Promise<string | null> {
    const how = await api.get<{method: string; url?: string}>(`/api/auth/v1/confirm?path=${encodeURIComponent(path)}&return=${encodeURIComponent(location.pathname)}`);
    if (how.method === 'oidc' && how.url) {
        if (await ask({title: texts.title, message: texts.provider, confirm: t('Continue')})) {
            beforeLeave?.();
            location.href = how.url;
        }
        return null;
    }
    if (how.method !== 'password' && how.method !== 'none') throw new Error(texts.impossible);
    let wrong = false;
    for (;;) {
        let password = '';
        if (how.method === 'password') {
            const pw = await askText({
                title: texts.title,
                message: (wrong ? t('The password was wrong.') + '\n\n' : '') + texts.message,
                input: {type: 'password', label: t('Password')},
                confirm: t('Confirm'),
            });
            if (pw === null) return null;
            password = pw;
        }
        try {
            return (await api.post<{ticket: string}>('/api/auth/v1/ticket', {path, confirm: true, password})).ticket;
        } catch (e) {
            if (e instanceof ApiError && e.status === 401 && how.method === 'password') {
                wrong = true;
                continue;
            }
            throw e;
        }
    }
}

/**
 * Back from the identity provider: the ticket, or the reason there is none, from the fragment -
 * which it removes. undefined when the page was not coming back from a confirmation.
 */
export function returned(): {ticket: string} | {refused: string} | undefined {
    const h = location.hash;
    if (!h.startsWith('#confirm=') && !h.startsWith('#confirm-error=')) return undefined;
    history.replaceState(history.state, '', location.pathname + location.search);
    if (h.startsWith('#confirm=')) return {ticket: decodeURIComponent(h.slice('#confirm='.length))};
    return {refused: decodeURIComponent(h.slice('#confirm-error='.length))};
}
