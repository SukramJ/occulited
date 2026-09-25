// Task 91: the API's refusal of a typed recovery key in the page's words. A checksum failure is a
// typo, never "wrong key"; the public half and a post-quantum identity get their own sentences.
import {ApiError} from './api';

export type Translate = (key: string, params?: Record<string, string | number>) => string;

export function recoveryKeyError(e: unknown, t: Translate): string {
    if (e instanceof ApiError) {
        switch (e.code) {
            case 'invalid-key':
                return t('There is a typo in the recovery key: check it against the emergency kit.');
            case 'is-recipient':
                return t('This is the public part (age1…); the recovery key is the code from the emergency kit or the line starting with AGE-SECRET-KEY-1.');
            case 'unsupported':
                return t('A post-quantum or plugin age identity is not supported here.');
            case 'wrong-key':
                return t('This backup is not encrypted to that key. Check the fingerprint the backup names against your emergency kits.');
            case 'corrupt':
                return t('The encrypted backup is damaged or was tampered with; nothing of it was kept. Use another copy.');
            case 'no-space':
                return t('Not enough free space to decrypt the backup beside the encrypted upload.');
        }
    }
    return (e as Error).message;
}
