// A QR code encoder for the key sheet (task 154): the device's own code, EQ01SG<SGTIN>DLK<key>,
// drawn again so a lost sticker has a scannable copy. ISO/IEC 18004 with error correction level M,
// versions 1 to 10 (up to 213 bytes, or 311 alphanumeric characters), alphanumeric mode when the
// text allows it, bytes (UTF-8) otherwise; the mask with the lowest penalty. Nothing leaves the
// page: the sheet's keys are drawn here and dropped when it closes.

const ALNUM = '0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ $%*+-./:';

// per version 1..10 at level M: codewords in all, error correction codewords per block, blocks
const RAW = [0, 26, 44, 70, 100, 134, 172, 196, 242, 292, 346];
const ECC_PER_BLOCK = [0, 10, 16, 26, 18, 24, 16, 18, 22, 22, 26];
const BLOCKS = [0, 1, 1, 1, 2, 2, 4, 4, 4, 5, 5];
const ALIGN: number[][] = [[], [], [6, 18], [6, 22], [6, 26], [6, 30], [6, 34], [6, 22, 38], [6, 24, 42], [6, 26, 46], [6, 28, 50]];
export const MAX_VERSION = 10;

function gfMul(x: number, y: number): number {
    let z = 0;
    for (let i = 7; i >= 0; i--) {
        z = (z << 1) ^ ((z >>> 7) * 0x11d);
        z ^= ((y >>> i) & 1) * x;
    }
    return z;
}

function rsDivisor(degree: number): number[] {
    const out = new Array<number>(degree).fill(0);
    out[degree - 1] = 1;
    let root = 1;
    for (let i = 0; i < degree; i++) {
        for (let j = 0; j < out.length; j++) {
            out[j] = gfMul(out[j] ?? 0, root) ^ (j + 1 < out.length ? (out[j + 1] ?? 0) : 0);
        }
        root = gfMul(root, 0x02);
    }
    return out;
}

function rsRemainder(data: number[], divisor: number[]): number[] {
    const out = divisor.map(() => 0);
    for (const b of data) {
        const factor = b ^ (out.shift() as number);
        out.push(0);
        divisor.forEach((c, i) => (out[i] = (out[i] ?? 0) ^ gfMul(c, factor)));
    }
    return out;
}

class Bits {
    bits: number[] = [];
    put(value: number, len: number) {
        for (let i = len - 1; i >= 0; i--) this.bits.push((value >>> i) & 1);
    }
}

/** The segment's bits: alphanumeric when every character allows it, UTF-8 bytes otherwise. */
function segment(text: string, version: number): {bits: number[]; fits: boolean} {
    const alnum = [...text].every((c) => ALNUM.includes(c));
    const b = new Bits();
    if (alnum) {
        b.put(0b0010, 4);
        b.put(text.length, version < 10 ? 9 : 11);
        const idx = (i: number) => ALNUM.indexOf(text.charAt(i));
        for (let i = 0; i + 1 < text.length; i += 2) b.put(idx(i) * 45 + idx(i + 1), 11);
        if (text.length % 2) b.put(idx(text.length - 1), 6);
        return {bits: b.bits, fits: text.length < (version < 10 ? 512 : 2048)};
    }
    const bytes = new TextEncoder().encode(text);
    b.put(0b0100, 4);
    b.put(bytes.length, version < 10 ? 8 : 16);
    for (const x of bytes) b.put(x, 8);
    return {bits: b.bits, fits: bytes.length < (version < 10 ? 256 : 65536)};
}

const at = (table: number[], v: number) => table[v] ?? 0;
const dataCapacity = (v: number) => at(RAW, v) - at(ECC_PER_BLOCK, v) * at(BLOCKS, v);

/** The codewords: data, terminator, padding, then the error correction, interleaved. */
function codewords(text: string): {version: number; words: number[]} {
    for (let v = 1; v <= MAX_VERSION; v++) {
        const seg = segment(text, v);
        const cap = dataCapacity(v) * 8;
        if (!seg.fits || seg.bits.length > cap) continue;
        const bits = seg.bits.slice();
        for (let i = 0; i < 4 && bits.length < cap; i++) bits.push(0);
        while (bits.length % 8) bits.push(0);
        const data: number[] = [];
        for (let i = 0; i < bits.length; i += 8) data.push(bits.slice(i, i + 8).reduce((a, x) => (a << 1) | x, 0));
        for (let pad = 0xec; data.length < dataCapacity(v); pad ^= 0xec ^ 0x11) data.push(pad);
        // the blocks: the short ones first, each with its error correction
        const n = at(BLOCKS, v);
        const ecc = at(ECC_PER_BLOCK, v);
        const short = n - (at(RAW, v) % n);
        const shortLen = Math.floor(at(RAW, v) / n);
        const div = rsDivisor(ecc);
        const blocks: number[][] = [];
        for (let i = 0, k = 0; i < n; i++) {
            const d = data.slice(k, k + shortLen - ecc + (i < short ? 0 : 1));
            k += d.length;
            const e = rsRemainder(d, div);
            if (i < short) d.push(0);
            blocks.push(d.concat(e));
        }
        const words: number[] = [];
        for (let i = 0; i < shortLen + 1; i++) {
            blocks.forEach((b, j) => {
                if (i !== shortLen - ecc || j >= short) words.push(b[i] ?? 0);
            });
        }
        return {version: v, words};
    }
    throw new Error('too long for a QR code of version ' + MAX_VERSION);
}

/** The code's modules, true for dark, without the quiet zone. */
export function qrMatrix(text: string): boolean[][] {
    const {version, words} = codewords(text);
    const size = version * 4 + 17;
    const g = new Grid(size);
    const set = (x: number, y: number, dark: boolean) => {
        g.dark[y * size + x] = dark ? 1 : 0;
        g.fn[y * size + x] = 1;
    };
    for (let i = 0; i < size; i++) {
        set(6, i, i % 2 === 0);
        set(i, 6, i % 2 === 0);
    }
    for (const [cx, cy] of [
        [3, 3],
        [size - 4, 3],
        [3, size - 4],
    ] as const) {
        for (let dy = -4; dy <= 4; dy++) {
            for (let dx = -4; dx <= 4; dx++) {
                const x = cx + dx;
                const y = cy + dy;
                const d = Math.max(Math.abs(dx), Math.abs(dy));
                if (x >= 0 && x < size && y >= 0 && y < size) set(x, y, d !== 2 && d !== 4);
            }
        }
    }
    const al = ALIGN[version] ?? [];
    al.forEach((ax, i) =>
        al.forEach((ay, j) => {
            const last = al.length - 1;
            if ((i === 0 && j === 0) || (i === 0 && j === last) || (i === last && j === 0)) return;
            for (let dy = -2; dy <= 2; dy++) {
                for (let dx = -2; dx <= 2; dx++) set(ax + dx, ay + dy, Math.max(Math.abs(dx), Math.abs(dy)) !== 1);
            }
        }),
    );
    const format = (mask: number) => {
        const data = (0b00 << 3) | mask; // level M
        let rem = data;
        for (let i = 0; i < 10; i++) rem = (rem << 1) ^ ((rem >>> 9) * 0x537);
        const bits = ((data << 10) | rem) ^ 0x5412;
        const bit = (i: number) => ((bits >>> i) & 1) === 1;
        for (let i = 0; i <= 5; i++) set(8, i, bit(i));
        set(8, 7, bit(6));
        set(8, 8, bit(7));
        set(7, 8, bit(8));
        for (let i = 9; i < 15; i++) set(14 - i, 8, bit(i));
        for (let i = 0; i < 8; i++) set(size - 1 - i, 8, bit(i));
        for (let i = 8; i < 15; i++) set(8, size - 15 + i, bit(i));
        set(8, size - 8, true);
    };
    format(0);
    if (version >= 7) {
        let rem = version;
        for (let i = 0; i < 12; i++) rem = (rem << 1) ^ ((rem >>> 11) * 0x1f25);
        const bits = (version << 12) | rem;
        for (let i = 0; i < 18; i++) {
            const dark = ((bits >>> i) & 1) === 1;
            const a = size - 11 + (i % 3);
            const b = Math.floor(i / 3);
            set(a, b, dark);
            set(b, a, dark);
        }
    }
    // the data, in two-column strips from the right, up and down in turn
    let i = 0;
    for (let right = size - 1; right >= 1; right -= 2) {
        if (right === 6) right = 5;
        for (let vert = 0; vert < size; vert++) {
            for (let j = 0; j < 2; j++) {
                const x = right - j;
                const y = ((right + 1) & 2) === 0 ? size - 1 - vert : vert;
                if (!g.fn[y * size + x] && i < words.length * 8) {
                    g.dark[y * size + x] = ((words[i >>> 3] ?? 0) >>> (7 - (i & 7))) & 1;
                    i++;
                }
            }
        }
    }
    const masks: ((x: number, y: number) => boolean)[] = [
        (x, y) => (x + y) % 2 === 0,
        (_x, y) => y % 2 === 0,
        (x) => x % 3 === 0,
        (x, y) => (x + y) % 3 === 0,
        (x, y) => (Math.floor(x / 3) + Math.floor(y / 2)) % 2 === 0,
        (x, y) => ((x * y) % 2) + ((x * y) % 3) === 0,
        (x, y) => (((x * y) % 2) + ((x * y) % 3)) % 2 === 0,
        (x, y) => (((x + y) % 2) + ((x * y) % 3)) % 2 === 0,
    ];
    const apply = (mask: (x: number, y: number) => boolean) => {
        for (let y = 0; y < size; y++) for (let x = 0; x < size; x++) if (!g.fn[y * size + x] && mask(x, y)) g.dark[y * size + x] = g.get(x, y) ? 0 : 1;
    };
    let best = 0;
    let bestScore = Infinity;
    masks.forEach((mask, k) => {
        apply(mask);
        format(k);
        const s = penalty(g);
        if (s < bestScore) {
            best = k;
            bestScore = s;
        }
        apply(mask);
    });
    apply(masks[best] ?? masks[0]!);
    format(best);
    return Array.from({length: size}, (_, y) => Array.from({length: size}, (_, x) => g.get(x, y)));
}

class Grid {
    dark: Uint8Array;
    fn: Uint8Array;
    constructor(public size: number) {
        this.dark = new Uint8Array(size * size);
        this.fn = new Uint8Array(size * size);
    }
    get(x: number, y: number): boolean {
        return this.dark[y * this.size + x] === 1;
    }
}

/** The standard's penalty: runs, 2×2 blocks, finder look-alikes, and the dark share. */
function penalty(g: Grid): number {
    const n = g.size;
    let score = 0;
    const line = (get: (i: number) => boolean) => {
        let run = 1;
        for (let i = 1; i <= n; i++) {
            if (i < n && get(i) === get(i - 1)) {
                run++;
                continue;
            }
            if (run >= 5) score += 3 + (run - 5);
            run = 1;
        }
        const look = [true, false, true, true, true, false, true];
        for (let i = 0; i + 7 <= n; i++) {
            if (!look.every((v, k) => get(i + k) === v)) continue;
            const lightBefore = i >= 4 && [1, 2, 3, 4].every((k) => !get(i - k));
            const lightAfter = i + 11 <= n && [7, 8, 9, 10].every((k) => !get(i + k));
            if (lightBefore || lightAfter) score += 40;
        }
    };
    for (let y = 0; y < n; y++) line((x) => g.get(x, y));
    for (let x = 0; x < n; x++) line((y) => g.get(x, y));
    let dark = 0;
    for (let y = 0; y < n; y++) {
        for (let x = 0; x < n; x++) {
            const c = g.get(x, y);
            if (c) dark++;
            if (y + 1 < n && x + 1 < n && c === g.get(x + 1, y) && c === g.get(x, y + 1) && c === g.get(x + 1, y + 1)) score += 3;
        }
    }
    const total = n * n;
    score += (Math.ceil(Math.abs(dark * 20 - total * 10) / total) - 1) * 10;
    return score;
}

/** The code as an SVG path in module units (the quiet zone of 4 around it), for a crisp print. */
export function qrSvg(text: string): {size: number; path: string} {
    const m = qrMatrix(text);
    const q = 4;
    let path = '';
    m.forEach((row, y) => row.forEach((dark, x) => dark && (path += `M${x + q} ${y + q}h1v1h-1z`)));
    return {size: m.length + 2 * q, path};
}
