// Reading a QR code from a picture (task 154's scanner, task 89's Wi-Fi codes): the browser's own
// BarcodeDetector where it has one, jsQR otherwise - loaded only when a code is read, so the page
// that never scans does not carry it. Text in, nothing else: what the text means is the caller's.

interface Detector {
    detect(source: CanvasImageSource): Promise<{rawValue: string}[]>;
}

let native: Promise<Detector | null> | null = null;

function nativeDetector(): Promise<Detector | null> {
    native ??= (async () => {
        const BD = (globalThis as unknown as {BarcodeDetector?: {new (o: {formats: string[]}): Detector; getSupportedFormats?: () => Promise<string[]>}}).BarcodeDetector;
        if (!BD) return null;
        try {
            const formats = (await BD.getSupportedFormats?.()) ?? ['qr_code'];
            return formats.includes('qr_code') ? new BD({formats: ['qr_code']}) : null;
        } catch {
            return null;
        }
    })();
    return native;
}

type JsQR = typeof import('jsqr').default;
let jsqr: Promise<JsQR> | null = null;
const loadJsQR = () => (jsqr ??= import('jsqr').then((m) => m.default));

/** The pixels of a source scaled so its longer side is at most max (0: as it is). */
function pixels(source: CanvasImageSource, w: number, h: number, max: number): ImageData | null {
    const f = max && Math.max(w, h) > max ? max / Math.max(w, h) : 1;
    const cw = Math.max(1, Math.round(w * f));
    const ch = Math.max(1, Math.round(h * f));
    const canvas = document.createElement('canvas');
    canvas.width = cw;
    canvas.height = ch;
    const ctx = canvas.getContext('2d', {willReadFrequently: true});
    if (!ctx) return null;
    ctx.drawImage(source, 0, 0, cw, ch);
    return ctx.getImageData(0, 0, cw, ch);
}

/**
 * One try on a picture: a camera frame, or a photo at one size. thorough also looks for a light
 * code on dark (slower; a photo, not a frame).
 */
export async function decodeSource(source: CanvasImageSource, w: number, h: number, opts: {max?: number; thorough?: boolean} = {}): Promise<string | null> {
    const det = await nativeDetector();
    if (det) {
        try {
            const found = await det.detect(source);
            if (found[0]?.rawValue) return found[0].rawValue;
        } catch {
            /* fall through to jsQR */
        }
        if (!opts.thorough) return null;
    }
    const img = pixels(source, w, h, opts.max ?? 1000);
    if (!img) return null;
    const read = await loadJsQR();
    return read(img.data, img.width, img.height, {inversionAttempts: opts.thorough ? 'attemptBoth' : 'dontInvert'})?.data ?? null;
}

/** A photo of a sticker: tried at a few sizes, since a small code in a large photo reads best
 *  scaled down and a close-up best near its own size. null when no size finds one. */
export async function decodeFile(file: Blob): Promise<string | null> {
    const bmp = await createImageBitmap(file);
    try {
        for (const max of [1200, 800, 1800, 500]) {
            const text = await decodeSource(bmp, bmp.width, bmp.height, {max, thorough: true});
            if (text) return text;
        }
        return null;
    } finally {
        bmp.close();
    }
}

/** Whether this page can show a live viewfinder: the camera is for secure contexts only. */
export const cameraAvailable = () => typeof window !== 'undefined' && window.isSecureContext && !!navigator.mediaDevices?.getUserMedia;
