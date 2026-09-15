<script lang="ts">
    // The FAB is the host the upload menu portals into: FeatureLayer renders
    // UploadMenu (its trigger + menu) into #upload-menu-root, and mobile.css
    // restyles that trigger into the 56px accent pill. Keeping the host mounted
    // (hidden via transform, never removed) means the portal never breaks when
    // the FAB slides away on other tabs or on scroll-down.
    interface Props {
        hidden?: boolean;
    }

    let { hidden = false }: Props = $props();
</script>

<div
    id="upload-menu-root"
    class="mobile-fab"
    class:is-hidden={hidden}
    aria-hidden={hidden ? 'true' : undefined}
    inert={hidden}
></div>
