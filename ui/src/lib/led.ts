// Task 95: the status LED page's model - the API's shapes and the pure steps the page takes on a
// configuration (moving a row, the look choices a row may have). No texts here: the page owns them,
// so the i18n check sees every key.

export interface LEDLook {
    color: string;
    pattern: string;
    color2?: string;
    /** task 134: the blink's off phase shows the normal colour instead of dark */
    over_normal?: boolean;
}

export interface LEDStateConfig extends Partial<LEDLook> {
    id: string;
    enabled: boolean;
}

export interface LEDConfig {
    enabled: boolean;
    normal: LEDLook;
    night: {enabled: boolean; from: string; to: string; show: 'errors' | 'off'};
    states: LEDStateConfig[];
    warnings_off: string[];
    addon_units: boolean;
    locate: {color: string; pattern: string; duration_s: number};
    external: {max_duration_s: number; allow_until_cleared: boolean};
    pwr_error_light: boolean;
}

export interface LEDView {
    available: boolean;
    reason?: string;
    hardware: {kind?: string; leds: string[]; colors: string[]; patterns: string[]; brightness: boolean};
    pwr: boolean;
    config: LEDConfig;
    defaults: LEDConfig;
    fixed: string[];
    error_states: string[];
    warning_ids: string[];
    /** task 95 (D-67): the hosts the no-internet check connects to, [] for none */
    internet_hosts?: string[];
}

export interface LEDOverride extends LEDLook {
    id: string;
    priority: 'low' | 'normal' | 'high';
    night: 'respect' | 'ignore';
    until?: string;
    by: string;
    since: string;
}

export interface LEDState {
    available: boolean;
    reason?: string;
    /** background: the colour the blink's off phase shows (task 134), absent for dark */
    shown: LEDLook & {source: string; id: string; detail?: string; since: string; background?: string};
    active: {id: string; since: string; detail?: string}[];
    overrides: LEDOverride[];
    locate?: LEDLook & {until: string};
    preview?: LEDLook & {until: string};
    night: boolean;
    booting: boolean;
    conflict: boolean;
    write_error?: string;
    pwr?: {enabled: boolean; error: boolean};
    capabilities: {colors: string[]; patterns: string[]; brightness: boolean};
}

/** The rows of the simple view, in the order the page lists them. */
export const SIMPLE_STATES = ['radio-down', 'no-network', 'service-failed', 'status-warning', 'system-update'];

/** Which of red, green, blue a colour lights. */
const CHANNELS: Record<string, [boolean, boolean, boolean]> = {
    off: [false, false, false],
    red: [true, false, false],
    green: [false, true, false],
    blue: [false, false, true],
    yellow: [true, true, false],
    cyan: [false, true, true],
    magenta: [true, false, true],
    white: [true, true, true],
};

/** Whether two colours share no channel - the pairs that can alternate. */
export function disjoint(a: string, b: string): boolean {
    const ca = CHANNELS[a];
    const cb = CHANNELS[b];
    if (!ca || !cb) return false;
    return !ca.some((on, i) => on && cb[i]);
}

/** The second colours a colour may alternate with: they light, and on other channels. */
export function secondColors(color: string, colors: string[]): string[] {
    return colors.filter((c) => c !== 'off' && c !== color && disjoint(color, c));
}

/** The patterns that can blink over the normal colour (task 134). */
export const OVER_NORMAL_PATTERNS = ['slow', 'fast', 'flash', 'double'];

/**
 * Whether a look can blink over the normal colour: 'yes', 'same' (the normal colour is the blink's
 * own, so the blink would not be seen and the page says so) or 'no' (the pattern has no off phase,
 * or the LED is dark).
 */
export function overNormalOption(l: Partial<LEDLook>, normal: LEDLook): 'yes' | 'same' | 'no' {
    if (!l.color || l.color === 'off' || !OVER_NORMAL_PATTERNS.includes(l.pattern ?? '')) return 'no';
    return normal.color === l.color ? 'same' : 'yes';
}

/** The colour a look's off phase shows on the box: the normal colour when it blinks over it visibly, '' for dark. */
export function backgroundOf(l: Partial<LEDLook>, normal: LEDLook, enabled = true): string {
    return enabled && l.over_normal && normal.color !== 'off' && overNormalOption(l, normal) === 'yes' ? normal.color : '';
}

/** A look in the API's one spelling: off is solid, a second colour only for alternate (and then a valid one), over_normal only set and only for a blinking pattern. */
export function normalizeLook<T extends Partial<LEDLook>>(l: T, colors: string[]): T {
    const out = {...l};
    if (!out.over_normal || out.color === 'off' || !OVER_NORMAL_PATTERNS.includes(out.pattern ?? '')) delete out.over_normal;
    if (out.color === 'off') {
        out.pattern = 'solid';
        delete out.color2;
        return out;
    }
    if (out.pattern !== 'alternate') {
        delete out.color2;
        return out;
    }
    const options = secondColors(out.color ?? '', colors);
    if (!out.color2 || !options.includes(out.color2)) out.color2 = options[0];
    if (!out.color2) out.pattern = 'solid';
    return out;
}

/** What the segmented control "when everything is fine" shows: blue, green, off, or custom. */
export function normalChoice(l: LEDLook): 'blue' | 'green' | 'off' | 'custom' {
    if (l.color === 'off') return 'off';
    if (l.pattern === 'solid' && (l.color === 'blue' || l.color === 'green')) return l.color;
    return 'custom';
}

/** Whether a stored configuration and the page's draft differ. */
export function changed(a: LEDConfig | null, b: LEDConfig | null): boolean {
    return JSON.stringify(a) !== JSON.stringify(b);
}

/** The CSS colour of an LED colour for the swatch. */
export const SWATCH: Record<string, string> = {
    red: '#e5252c',
    green: '#22b24a',
    blue: '#2474e8',
    yellow: '#f1c21b',
    cyan: '#1cc6d3',
    magenta: '#cf3ad6',
    white: '#f5f5f5',
    off: 'transparent',
};
