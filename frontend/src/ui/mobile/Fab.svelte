<script lang="ts">
    import { openNewFolderModal } from '../../modules/modals/folder';
    import { chooseFilesForCurrentFolder, chooseFolderForCurrentFolder } from '../../modules/transfers';
    import UploadMenu from '../chrome/UploadMenu.svelte';

    interface Props {
        // The menu installs window and document listeners and, on a phone, opens
        // as a modal sheet. Mounting it before the dashboard is up would put
        // those behind the login screen, so the shell passes its own visibility
        // down rather than letting the button exist on its own.
        dashboardVisible: boolean;
    }

    let { dashboardVisible }: Props = $props();
</script>

<!-- The upload menu renders its own trigger, and mobile.css restyles that
     trigger into the docked accent circle through .mobile-fab, so this wrapper
     is the selector every one of those rules hangs off and stays exactly as it
     was. Only the id a portal aimed at is gone.

     Unlike desktop, the phone menu also creates a folder: Files has no context
     menu to reach that from, so the one creation control has to offer both.
     Android offers the folder upload too, because the app's own bridge walks
     the chosen tree and the upload copies out of it a window at a time, so the
     item is no longer a button that could only fail. See
     modules/android-folder.ts. -->
<div class="mobile-fab">
    {#if dashboardVisible}
        <UploadMenu
            onFiles={chooseFilesForCurrentFolder}
            onFolder={chooseFolderForCurrentFolder}
            onNewFolder={openNewFolderModal}
        />
    {/if}
</div>
