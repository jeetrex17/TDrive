export interface AppFileTarget {
    id: number;
    name: string;
    size: number;
    encrypted?: boolean;
}

export interface RefreshFilesOptions {
    background?: boolean;
}

export interface AppActions {
    refreshFiles: (options?: RefreshFilesOptions) => void;
    triggerRefresh: () => Promise<void>;
    openFile: (target: AppFileTarget) => Promise<void>;
    playVideo: (target: AppFileTarget) => Promise<void>;
}

let configuredActions: AppActions | null = null;

export function configureAppActions(actions: AppActions): void {
    configuredActions = actions;
}

export function appActions(): Readonly<AppActions> {
    if (!configuredActions) throw new Error('App actions are not configured');
    return configuredActions;
}
