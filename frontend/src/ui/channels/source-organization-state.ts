import { get, writable } from 'svelte/store';
import { listSourceGroups } from '../../api';
import { closeDriveSwitcher } from '../mobile/mobile-shell-store';
import { sourceKey, type ChannelSource } from './channel-model';

export interface SourceGroup {
    readonly id: string;
    readonly name: string;
}

export interface SourceGroupAssignment {
    readonly peer_kind: string;
    readonly peer_id: number;
    readonly generation: string;
    readonly group_id: string;
}

interface SourceOrganization {
    readonly groups: readonly SourceGroup[];
    readonly assignments: readonly SourceGroupAssignment[];
}

interface OrganizationView extends SourceOrganization {
    readonly accountId: string;
    readonly status: 'idle' | 'loading' | 'ready' | 'error';
}

interface OrganizationRequest {
    readonly accountId: string;
    readonly source?: ChannelSource;
}

const empty = { accountId: '', status: 'idle', groups: [], assignments: [] } as const;
const organization = writable<OrganizationView>(empty);
export const sourceOrganization = { subscribe: organization.subscribe };
export const sourceOrganizationRequest = writable<OrganizationRequest | null>(null);
let revision = 0;

export async function loadSourceOrganization(accountId: string, retry = false): Promise<void> {
    if (!accountId || accountId === '0') return;
    if (!retry && get(organization).accountId === accountId) return;
    const version = ++revision;
    organization.set({ ...empty, accountId, status: 'loading' });
    try {
        const data = await listSourceGroups(accountId);
        if (version === revision) publishSourceOrganization(accountId, data);
    } catch {
        if (version === revision) organization.set({ ...empty, accountId, status: 'error' });
    }
}

export function publishSourceOrganization(accountId: string, data: {
    groups: readonly SourceGroup[] | null;
    assignments: readonly SourceGroupAssignment[] | null;
}): void {
    if (get(organization).accountId !== accountId) return;
    revision += 1;
    organization.set({ accountId, status: 'ready', groups: data.groups ?? [], assignments: data.assignments ?? [] });
}

export function openSourceOrganization(source: ChannelSource): void {
    openOrganization({ accountId: source.accountId, source });
}

export function openSourceOrganizationManager(accountId: string): void {
    openOrganization({ accountId });
}

function openOrganization(request: OrganizationRequest): void {
    if (!request.accountId || request.accountId === '0') return;
    closeDriveSwitcher();
    sourceOrganizationRequest.set(request);
    void loadSourceOrganization(request.accountId);
}

export function closeSourceOrganization(): void {
    sourceOrganizationRequest.set(null);
}

export function resetSourceOrganization(): void {
    revision += 1;
    closeSourceOrganization();
    organization.set(empty);
}

function assignmentKey(accountId: string, assignment: SourceGroupAssignment): string {
    return `${accountId}:${assignment.peer_kind}:${assignment.peer_id}:${assignment.generation}`;
}

export function groupForSource(source: ChannelSource, view: OrganizationView): string {
    if (source.accountId !== view.accountId) return '';
    const key = sourceKey(source);
    return view.assignments.find((assignment) => assignmentKey(view.accountId, assignment) === key)?.group_id ?? '';
}

export function partitionSourceGroups(sources: readonly ChannelSource[], view: OrganizationView) {
    const grouped = new Map(view.groups.map((group) => [group.id, { ...group, sources: [] as ChannelSource[] }]));
    const assignments = new Map(view.assignments.map((item) => [assignmentKey(view.accountId, item), item.group_id]));
    const ungrouped: ChannelSource[] = [];
    for (const source of sources) {
        const id = assignments.get(sourceKey(source));
        const destination = id ? grouped.get(id) : undefined;
        if (destination) destination.sources.push(source);
        else ungrouped.push(source);
    }
    return { groups: Array.from(grouped.values()), ungrouped };
}
