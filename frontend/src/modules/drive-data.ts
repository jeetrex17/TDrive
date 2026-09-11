import { getFolderStats } from '../api';
import type { FolderStat } from '../types';

export async function calculateVisibleFolderStats(parentId: string): Promise<Map<string, FolderStat>> {
    const stats = await getFolderStats(parentId);
    return new Map(stats.map((entry) => [entry.id, entry]));
}
