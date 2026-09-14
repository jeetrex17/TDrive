import { derived, writable, type Readable } from 'svelte/store';
import type { AppError } from '../../modules/errors';

export type AuthScreen = 'setup' | 'phone' | 'code' | 'password' | 'drive';

export interface AppLifecycle {
    start(): Promise<void>;
    stop(): void;
}

export type AppView =
    | { kind: 'startup' }
    | { kind: 'loading'; message: string }
    | { kind: 'auth'; screen: AuthScreen }
    | { kind: 'dashboard' }
    | { kind: 'fatal'; error: AppError };

const appViewState = writable<AppView>({ kind: 'startup' });

export const appView: Readable<AppView> = {
    subscribe: appViewState.subscribe,
};

export const authScreen: Readable<AuthScreen | null> = derived(
    appViewState,
    (view) => view.kind === 'auth' ? view.screen : null,
);

export function showStartupView(): void {
    appViewState.set({ kind: 'startup' });
}

export function showLoadingView(message = 'Opening TDrive…'): void {
    appViewState.set({ kind: 'loading', message });
}

export function showAuthView(screen: AuthScreen): void {
    appViewState.set({ kind: 'auth', screen });
}

export function showDashboardView(): void {
    appViewState.set({ kind: 'dashboard' });
}

export function showFatalView(error: AppError): void {
    appViewState.set({ kind: 'fatal', error });
}
