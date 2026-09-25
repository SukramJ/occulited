<script lang="ts">
    // The shared QR scanner (task 154, reused by task 89's Wi-Fi codes): a photo of the code,
    // decoded in the browser (works over plain HTTP; on a phone the file picker offers the camera
    // app), and a live viewfinder where the page is a secure context (HTTPS), which the camera
    // requires. It turns a picture into text and nothing more: the caller decides what the text
    // is. With continuous the viewfinder stays open for the next code (a pile of stickers); the
    // same code is not reported twice in a row.
    import {onDestroy, tick} from 'svelte';
    import {t} from './i18n.svelte';
    import {cameraAvailable, decodeFile, decodeSource} from './qrdecode';

    interface Props {
        /** a code was read; true when it was taken (the single-shot viewfinder closes then) */
        onresult: (text: string) => boolean | Promise<boolean>;
        continuous?: boolean;
        /** the photo button's label */
        photoLabel?: string;
        disabled?: boolean;
    }
    let {onresult, continuous = false, photoLabel, disabled = false}: Props = $props();

    let file = $state<HTMLInputElement | null>(null);
    let video = $state<HTMLVideoElement | null>(null);
    let stream: MediaStream | null = null;
    let live = $state(false);
    let reading = $state(false);
    let note = $state('');
    let last = '';
    let lastAt = 0;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const canCamera = cameraAvailable();

    async function report(text: string): Promise<boolean> {
        reading = true;
        try {
            return await onresult(text);
        } finally {
            reading = false;
        }
    }

    async function photo(e: Event) {
        const input = e.currentTarget as HTMLInputElement;
        const f = input.files?.[0];
        input.value = '';
        if (!f) return;
        note = t('Reading the photo…');
        try {
            const text = await decodeFile(f);
            note = text ? '' : t('No QR code found in the photo. Take it closer and sharper, with the whole code in the picture.');
            if (text) await report(text);
        } catch (err) {
            note = (err as Error).message;
        }
    }

    async function start() {
        note = '';
        try {
            stream = await navigator.mediaDevices.getUserMedia({video: {facingMode: 'environment'}, audio: false});
        } catch (err) {
            note = t('The camera could not be opened: {reason}', {reason: (err as Error).message});
            return;
        }
        live = true;
        await tick();
        if (!video) return;
        video.srcObject = stream;
        await video.play().catch(() => undefined);
        next();
    }

    function stop() {
        clearTimeout(timer);
        stream?.getTracks().forEach((tr) => tr.stop());
        stream = null;
        live = false;
    }

    function next() {
        timer = setTimeout(async () => {
            if (!live || !video) return;
            if (!reading && video.readyState >= 2 && video.videoWidth) {
                const text = await decodeSource(video, video.videoWidth, video.videoHeight, {max: 800}).catch(() => null);
                const now = Date.now();
                if (text && (text !== last || now - lastAt > 4000)) {
                    last = text;
                    lastAt = now;
                    const taken = await report(text);
                    if (taken && !continuous) {
                        stop();
                        return;
                    }
                }
            }
            next();
        }, 250);
    }

    onDestroy(stop);
</script>

<div class="qr-scanner">
    <div class="ol-actions qr-buttons">
        <button type="button" class="hmm-button" {disabled} onclick={() => file?.click()} data-qr="photo">{photoLabel ?? t('Photo of the code')}</button>
        <input bind:this={file} type="file" accept="image/*" hidden onchange={photo} data-qr="file" />
        {#if canCamera}
            {#if live}
                <button type="button" class="hmm-button" onclick={stop} data-qr="stop">{t('Close the camera')}</button>
            {:else}
                <button type="button" class="hmm-button" {disabled} onclick={start} data-qr="camera">{t('Camera')}</button>
            {/if}
        {/if}
    </div>
    {#if !canCamera}<p class="ol-muted qr-hint" data-qr="no-camera">{t('The live camera needs the page over HTTPS; a photo works here.')}</p>{/if}
    {#if live}
        <div class="qr-view">
            <!-- svelte-ignore a11y_media_has_caption -->
            <video bind:this={video} playsinline muted></video>
            <div class="qr-frame" aria-hidden="true"></div>
        </div>
        <p class="ol-muted qr-hint">{continuous ? t('Hold one code after another in front of the camera.') : t('Hold the code in front of the camera.')}</p>
    {/if}
    {#if note}<p class="ol-muted qr-note" data-qr="note">{note}</p>{/if}
</div>

<style>
    .qr-buttons { margin: 0; }
    .qr-hint, .qr-note { margin: 4px 0 0; font-size: 0.92em; }
    .qr-view { position: relative; margin-top: 8px; max-width: 420px; border-radius: 6px; overflow: hidden; background: var(--ol-paper-ink); }
    .qr-view video { display: block; width: 100%; height: auto; }
    .qr-frame { position: absolute; inset: 18%; border: 2px solid rgba(255, 255, 255, 0.85); border-radius: 8px; box-shadow: 0 0 0 999px rgba(0, 0, 0, 0.25); pointer-events: none; }
</style>
