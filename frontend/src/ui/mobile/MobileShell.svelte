<script lang="ts">
    import { onMount } from 'svelte';
    import { get } from 'svelte/store';
    // Aliased: a local binding named `state` would make the `$state` rune read as
    // a store subscription (Svelte 5 ambiguity), which breaks the shell at render.
    import { state as appState } from '../../state';
    import { navigateToIndex } from '../../modules/navigation';
    import { enterPhotos, exitPhotos } from '../../modules/gallery';
    import { selectionBarState } from '../selection/selection-bar-store';
    import FeatureLayer from '../app/FeatureLayer.svelte';
    import AccountTab from './AccountTab.svelte';
    import DriveSwitcherSheet from './DriveSwitcherSheet.svelte';
    import Fab from './Fab.svelte';
    import TabBar from './TabBar.svelte';
    import TopBar from './TopBar.svelte';
    import TransfersTab from './TransfersTab.svelte';
    import { activateMobileBack } from './mobile-back';
    import { activateSafeArea } from './safe-area';
    import { activateKeyboardInsets } from './keyboard-insets';
    import { activeTab, transferAttentionCount, type MobileTab } from './mobile-shell-store';
    import { sidebarState } from '../sidebar/sidebar-store';
    import { breadcrumbPath } from '../chrome/breadcrumb-store';

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

    function scrollActiveToTop(tab: MobileTab): void {
        const el = tab === 'files'
            ? document.getElementById('file-list')
            : tab === 'photos'
                ? document.getElementById('gallery-view')
                : document.querySelector<HTMLElement>(`.mobile-panel[data-tab="${tab}"] .mobile-scroll`);
        el?.scrollTo?.({ top: 0, behavior: 'smooth' });
    }

    // The top bar carries no divider until content actually passes under it.
    // A permanent hairline plus the card's own edge draws two separators a few
    // pixels apart; the bar only needs one once there is something to separate.
    let scrolled = $state(false);

    onMount(() => {
        const disposeBack = activateMobileBack();
        const disposeSafeArea = activateSafeArea();
        const disposeKeyboard = activateKeyboardInsets();

        const list = document.getElementById('file-list');
        const onScroll = (): void => {
            scrolled = (list?.scrollTop ?? 0) > 2;
        };
        list?.addEventListener('scroll', onScroll, { passive: true });

        return () => {
            list?.removeEventListener('scroll', onScroll);
            disposeBack();
            disposeSafeArea();
            disposeKeyboard();
            clearTimeout(navTimer);
        };
    });
</script>

<div
    id="success-screen"
    class="mobile-shell"
    class:is-scrolled={scrolled}
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
            <div id="gallery-view" class="gallery-view" tabindex="-1" aria-label="Photos"></div>
        </main>

        <div class="mobile-panel" data-tab="transfers" hidden={$activeTab !== 'transfers'}>
            <TransfersTab />
        </div>
        <div class="mobile-panel" data-tab="account" hidden={$activeTab !== 'account'}>
            <AccountTab />
        </div>
    </div>

    <!-- Selection bar host; the controller flips its display and fills it via a
         portal. It sits above the tab bar and replaces it while selecting. -->
    <div
        id="selection-bar"
        class="selection-bar mobile-selection-bar"
        style="display: none;"
        role="status"
        aria-live="polite"
    ></div>

    <!-- The upload button is docked into the tab bar, so it lives in the same
         slot and never slides away on scroll: it is part of the bar's shape,
         and a gap opening and closing in the middle of it would read as a
         glitch rather than a hint. -->
    <div class="mobile-tabbar-slot" hidden={selecting}>
        <TabBar active={$activeTab} transferBadge={$transferAttentionCount} onSelect={selectTab} />
        <Fab />
    </div>

    <DriveSwitcherSheet />

    <!-- Hosts the imperative controllers still write to but the phone layout does
         not surface (the bell lives in the Transfers tab, the profile in Account,
         folder position in the top bar). Kept mounted so nothing breaks. -->
    <div class="mobile-hidden-hosts">
        <div id="notif-bell-root"></div>
        <div id="profile-root"></div>
        <div id="breadcrumb-root"></div>
        <span id="storage-used"></span>
    </div>
</div>

<FeatureLayer />
