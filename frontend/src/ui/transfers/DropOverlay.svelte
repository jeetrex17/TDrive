<script lang="ts">
    import CloudUploadIcon from '@lucide/svelte/icons/cloud-upload';
</script>

<div id="drop-overlay" class="drop-overlay" aria-hidden="true" hidden>
    <div class="drop-overlay-card">
        <CloudUploadIcon size={30} aria-hidden="true" />
        <div id="drop-overlay-title">Drop files to upload</div>
    </div>
</div>
