// openccu-lite task 228: the Format dialog's filesystem and label. A label is required - the
// journal and the backup targets find a stick by it - and must suit the filesystem: exFAT takes 15
// characters, ext4 16, both letters, digits, - and _ (what udev's ID_FS_LABEL keeps as it is).

export type FS = 'exfat' | 'ext4';

export const LABEL_MAX: Record<FS, number> = {exfat: 15, ext4: 16};

const SAFE = /^[A-Za-z0-9_-]+$/;

/** the reason a label does not do, as an i18n key with {n}; '' when it does */
export function labelError(fs: FS, label: string): string {
    if (!label) return 'A label is required: the journal and the backups find the stick by it.';
    if (label.length > LABEL_MAX[fs]) return 'At most {n} characters.';
    if (!SAFE.test(label)) return 'Letters, digits, - and _ only.';
    return '';
}

/** the label the dialog starts with: the stick's current one when it suits fs, else OPENCCU */
export function suggestLabel(current: string[], fs: FS): string {
    for (const c of current) {
        const l = c.trim();
        if (l && !labelError(fs, l)) return l;
        const cleaned = l.replace(/[^A-Za-z0-9_-]+/g, '_').replace(/^_+|_+$/g, '').slice(0, LABEL_MAX[fs]);
        if (cleaned && !labelError(fs, cleaned)) return cleaned;
    }
    return 'OPENCCU';
}
