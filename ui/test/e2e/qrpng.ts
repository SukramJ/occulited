// A photo of a QR code for the scanner's tests (task 154, task 89): the code drawn by the key
// sheet's encoder as a greyscale PNG with a margin, as a phone's picture of a sticker would be.
import {writeFileSync} from 'node:fs';
import {deflateSync} from 'node:zlib';
import {qrMatrix} from '../../src/lib/qrencode';

function crc32(buf: Buffer): number {
    let c = ~0;
    for (const b of buf) {
        c ^= b;
        for (let k = 0; k < 8; k++) c = (c >>> 1) ^ (0xedb88320 & -(c & 1));
    }
    return ~c >>> 0;
}
export function qrPNG(text: string, scale = 8, margin = 60): Buffer {
    const m = qrMatrix(text);
    const n = m.length * scale + 2 * margin;
    const raw = Buffer.alloc((n + 1) * n, 255);
    for (let y = 0; y < n; y++) {
        raw[y * (n + 1)] = 0; // filter: none
        for (let x = 0; x < n; x++) {
            const my = Math.floor((y - margin) / scale);
            const mx = Math.floor((x - margin) / scale);
            if (my >= 0 && mx >= 0 && my < m.length && mx < m.length && m[my]![mx]) raw[y * (n + 1) + 1 + x] = 0;
        }
    }
    const chunk = (type: string, data: Buffer) => {
        const len = Buffer.alloc(4);
        len.writeUInt32BE(data.length);
        const td = Buffer.concat([Buffer.from(type, 'ascii'), data]);
        const crc = Buffer.alloc(4);
        crc.writeUInt32BE(crc32(td));
        return Buffer.concat([len, td, crc]);
    };
    const ihdr = Buffer.alloc(13);
    ihdr.writeUInt32BE(n, 0);
    ihdr.writeUInt32BE(n, 4);
    ihdr[8] = 8; // bit depth
    ihdr[9] = 0; // greyscale
    return Buffer.concat([Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]), chunk('IHDR', ihdr), chunk('IDAT', deflateSync(raw)), chunk('IEND', Buffer.alloc(0))]);
}

/** The same code as a camera would film it: a Y4M file (one frame; Chromium's fake camera loops it). */
export function qrY4M(text: string, path: string, scale = 6, margin = 80): string {
    const m = qrMatrix(text);
    let n = m.length * scale + 2 * margin;
    n += n % 2; // 4:2:0 wants even sides
    const y = Buffer.alloc(n * n, 235);
    for (let row = 0; row < n; row++) {
        for (let col = 0; col < n; col++) {
            const my = Math.floor((row - margin) / scale);
            const mx = Math.floor((col - margin) / scale);
            if (my >= 0 && mx >= 0 && my < m.length && mx < m.length && m[my]![mx]) y[row * n + col] = 16;
        }
    }
    const uv = Buffer.alloc((n / 2) * (n / 2) * 2, 128);
    writeFileSync(path, Buffer.concat([Buffer.from(`YUV4MPEG2 W${n} H${n} F10:1 Ip A1:1 C420jpeg\nFRAME\n`), y, uv]));
    return path;
}
