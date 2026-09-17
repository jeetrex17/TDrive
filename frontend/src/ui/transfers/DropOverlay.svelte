<script lang="ts">
    import CloudUploadIcon from '@lucide/svelte/icons/cloud-upload';
    import { dropOverlayState } from './drop-overlay-store';

    // The box arrives as four numbers rather than a ready-made style string so
    // the controller never has to think in CSS, and so a state that has not
    // been measured yet renders as no positioning at all rather than as
    // "undefinedpx".
    const boxStyle = $derived.by(() => {
        const box = $dropOverlayState.box;
        if (!box) return '';
        return `top: ${box.top}px; left: ${box.left}px; width: ${box.width}px; height: ${box.height}px;`;
    });
</script>

<!-- The overlay never takes pointer events, so the drop still lands on the file
     list underneath it and the Go-side importer stays the single consumer. -->
<div
    class="drop-overlay"
    class:is-visible={$dropOverlayState.visible}
    hidden={!$dropOverlayState.present}
    style={boxStyle}
    aria-hidden="true"
>
    <div class="drop-overlay-card">
        <CloudUploadIcon size={30} aria-hidden="true" />
        <div class="drop-overlay-title">{$dropOverlayState.title}</div>
    </div>
</div>
