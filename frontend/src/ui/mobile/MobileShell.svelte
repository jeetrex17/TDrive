<script lang="ts">
    import { onMount } from 'svelte';
    import { get } from 'svelte/store';
    // Aliased: a local binding named `state` would make the `$state` rune read as
    // a store subscription (Svelte 5 ambiguity), which breaks the shell at render.
    import { state as appState } from '../../state';
    import { navigateToIndex } from '../../modules/navigation';
    import { enterPhotos, exitPhotos } from '../../modules/gallery';
    import { clearSelection, openSelectedItemsDelete, openSelectedItemsMove } from '../../modules/selection';
    import { selectionBarState } from '../selection/selection-bar-store';
    import FeatureLayer from '../app/FeatureLayer.svelte';
    import Gallery from '../gallery/Gallery.svelte';
    import SelectionBar from '../selection/SelectionBar.svelte';
    import AccountTab from './AccountTab.svelte';
    import Fab from './Fab.svelte';
    import DriveSwitcherSheet from './DriveSwitcherSheet.svelte';
    import TabBar from './TabBar.svelte';
    import TopBar from './TopBar.svelte';
    import TransfersTab from './TransfersTab.svelte';
    import { activateMobileBack } from './mobile-back';
    import { activateSafeArea } from './safe-area';
    import { activateKeyboardInsets } from './keyboard-insets';
    import { activateBackgroundTransfers } from './background-transfers';
    import { scrollBehavior } from './motion';
    import { activeTab, keyboardOpen, transferAttentionCount, type MobileTab } from './mobile-shell-store';
    import { sidebarState } from '../sidebar/sidebar-store';
    import { breadcrumbPath } from '../chrome/breadcrumb-store';
    import { fileListView } from '../file-list/file-list-store';

    interface Props {
        dashboardVisible: boolean;
    }

    let { dashboardVisible }: Props = $props();

    // Selecting rows swaps the tab bar for the selection bar (spec 2.5); the
    // count comes from the shared selection store the controller feeds.
    const selecting = $derived($selectionBarState.count > 0);
    // Files and Photos share one content region (the file list vs the gallery);
    // Transfers and Account are their own panels.
    const showMain = $derived($activeTab === 'files' || $activeTab === 'photos');
    // An empty Files view already places Upload and Create folder in its
    // center. Keep that focused state in charge rather than floating a second
    // creation control over it.
    const emptyFilesOwnsCreation = $derived(
        $fileListView.kind === 'state'
        && $fileListView.stateKind === 'empty'
        && Boolean($fileListView.actionLabel || $fileListView.secondaryActionLabel),
    );
    const showContextAction = $derived(
        $activeTab === 'photos' || ($activeTab === 'files' && !emptyFilesOwnsCreation),
    );
    const contextualActionVisible = $derived(showContextAction && !selecting && !$keyboardOpen);

    // Publish the upload button's footprint so anything else that floats over
    // the content can stand off it. The toast stack is the one that matters:
    // the notice a tap on Upload raises was landing on the button that raised
    // it. A custom property rather than a class because the surfaces that read
    // it are not all inside this shell -- FeatureLayer hosts the toasts as a
    // sibling -- and :root is the one ancestor they share.
    $effect(() => {
        const root = document.documentElement.style;
        root.setProperty(
            '--mobile-fab-clearance',
            contextualActionVisible ? 'var(--context-action-clearance)' : '0px',
        );
        return () => root.removeProperty('--mobile-fab-clearance');
    });

    // The gallery owns whether Photos is showing; the tab is only how the user
    // asked for it. Hardware BACK and in-app navigation leave the gallery
    // without touching the tab bar, so follow the gallery whenever the two
    // disagree, or the Photos tab would stay lit over the file list.
    $effect(() => {
        const wanted: MobileTab = $sidebarState.photosActive ? 'photos' : 'files';
        const current = get(activeTab);
        if ((current === 'files' || current === 'photos') && current !== wanted) {
            activeTab.set(wanted);
        }
    });

    // Walking into a folder pushes from the right and coming back pops to it,
    // so the list carries the same sense of depth the back chevron promises.
    // Only the direction lives here; the movement itself is CSS, which keeps it
    // off the main thread and lets prefers-reduced-motion drop it wholesale.
    let navDirection = $state<'forward' | 'back' | null>(null);
    let lastDepth = -1;
    let navTimer: ReturnType<typeof setTimeout> | undefined;

    $effect(() => {
        const depth = $breadcrumbPath.length;
        if (lastDepth >= 0 && depth !== lastDepth) {
            navDirection = depth > lastDepth ? 'forward' : 'back';
            clearTimeout(navTimer);
            // Cleared so a re-render mid-animation does not replay it.
            navTimer = setTimeout(() => { navDirection = null; }, 280);
        }
        lastDepth = depth;
    });

    function selectTab(tab: MobileTab): void {
        const reselect = get(activeTab) === tab;
        // Photos is a view of the same drive, so entering/leaving it toggles the
        // existing gallery virtual view rather than navigating away.
        if (tab === 'files') {
            if (appState.virtualView === 'photos') exitPhotos();
        } else if (tab === 'photos') {
            enterPhotos();
        }
        activeTab.set(tab);
        if (reselect) resetTab(tab);
    }

    // Re-tapping the active tab returns to the drive root and scrolls to top
    // (interactivity-13): a predictable home base from anywhere in a drive.
    function resetTab(tab: MobileTab): void {
        if (tab === 'files') navigateToIndex(-1);
        scrollActiveToTop(tab);
    }

    // Each tab has its own scrolling surface; Files and Photos swap theirs for
    // one another inside the shared content region.
    function activeScroller(tab: MobileTab): HTMLElement | null {
        if (tab === 'files') return document.getElementById('file-list');
        if (tab === 'photos') return document.getElementById('gallery-view');
        return document.querySelector<HTMLElement>(`.mobile-panel[data-tab="${tab}"] .mobile-scroll`);
    }

    function scrollActiveToTop(tab: MobileTab): void {
        activeScroller(tab)?.scrollTo?.({ top: 0, behavior: scrollBehavior() });
    }

    // The top bar carries no divider until content actually passes under it.
    // A permanent hairline plus the card's own edge draws two separators a few
    // pixels apart; the bar only needs one once there is something to separate.
    let scrolled = $state(false);

    // Read from whichever surface is showing. Wired to the file list alone, the
    // rule stayed on under the Account tab because the list it was watching had
    // been left part-scrolled, and never came on at all while the gallery moved.
    $effect(() => {
        const surface = activeScroller($activeTab);
        const onScroll = (): void => {
            scrolled = (surface?.scrollTop ?? 0) > 2;
        };
        onScroll();
        surface?.addEventListener('scroll', onScroll, { passive: true });
        return () => surface?.removeEventListener('scroll', onScroll);
    });

    onMount(() => {
        const disposeBack = activateMobileBack();
        const disposeSafeArea = activateSafeArea();
        const disposeKeyboard = activateKeyboardInsets();
        const disposeBackground = activateBackgroundTransfers();

        return () => {
            disposeBack();
            disposeSafeArea();
            disposeKeyboard();
            disposeBackground();
            clearTimeout(navTimer);
        };
    });
</script>

<div
    id="success-screen"
    class="mobile-shell"
    class:is-scrolled={scrolled}
    class:is-selecting={selecting}
    class:has-context-action={contextualActionVisible}
    hidden={!dashboardVisible}
    aria-hidden={dashboardVisible ? undefined : 'true'}
>
    <TopBar active={$activeTab} />

    <div class="mobile-body">
        <!-- Files and Photos share this region; the gallery controller toggles
             .photos-mode on it to swap the list for the grid. -->
        <main class="main-content mobile-panel" data-tab="files" hidden={!showMain}>
            <div id="gallery-title" class="gallery-title">Photos</div>
            <div
                id="file-list"
                class="file-list-box"
                data-file-drop-target
                data-nav={navDirection ?? undefined}
                role="grid"
                aria-label="Files"
                aria-multiselectable="true"
                aria-colcount="4"
            ></div>
            <!-- #gallery-view is the gallery's scroll container, not a seam: the
                 component measures it, the thumbnail controller uses it as its
                 IntersectionObserver root, and the click delegation, the preview
                 modal's return-to-grid and this shell's scroll-to-top all look
                 it up by this id. So the element stays exactly as it was and only the
                 portal that filled it from elsewhere is gone. Held back until the
                 dashboard is up so the grid's window listeners and resize observer
                 live no longer than the dashboard does. -->
            <div id="gallery-view" class="gallery-view" tabindex="-1" aria-label="Photos">
                {#if dashboardVisible}
                    <Gallery />
                {/if}
            </div>
        </main>

        <div class="mobile-panel" data-tab="transfers" hidden={$activeTab !== 'transfers'}>
            <TransfersTab />
        </div>
        <div class="mobile-panel" data-tab="account" hidden={$activeTab !== 'account'}>
            <AccountTab />
        </div>
    </div>

    <!-- The selection bar sits above the tab bar and replaces it while
         selecting. It keeps its id and its inline display: none because the
         selection controller is what reveals it, by flipping that style once a
         row is picked; the bar is fixed to the bottom of the viewport from
         its own stylesheet, so where it sits in this shell does not move it. -->
    <div
        id="selection-bar"
        class="selection-bar mobile-selection-bar"
        style="display: none;"
        role="status"
        aria-live="polite"
    >
        {#if dashboardVisible}
            <SelectionBar
                onMove={openSelectedItemsMove}
                onDelete={openSelectedItemsDelete}
                onClear={clearSelection}
            />
        {/if}
    </div>

    <!-- Upload/Create is contextual to drive content, not a fifth destination.
         It stays above the navigation bar on Files and Photos, where a new
         item has a meaningful destination, and leaves Transfers and Account
         calm. -->
    <div class="mobile-context-action" hidden={!contextualActionVisible}>
        <Fab {dashboardVisible} />
    </div>

    <div class="mobile-tabbar-slot" hidden={selecting || $keyboardOpen}>
        <TabBar active={$activeTab} transferBadge={$transferAttentionCount} onSelect={selectTab} />
    </div>

    <DriveSwitcherSheet />

</div>

<FeatureLayer />
