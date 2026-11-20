import {
    DeleteSourceGroup,
    ListSourceGroups,
    MoveSourceToGroup,
    SaveSourceGroup,
} from '../../../bindings/TDrive/internal/app/driveservice';
import { invokeBackend } from '../../api/gateway';
import type { ChannelSource } from './channel-model';

export const listSourceGroups = (accountId: string) => invokeBackend(ListSourceGroups, Number(accountId));

export const saveSourceGroup = (accountId: string, groupId: string, name: string) =>
    invokeBackend(SaveSourceGroup, Number(accountId), groupId, name);

export const deleteSourceGroup = (accountId: string, groupId: string) =>
    invokeBackend(DeleteSourceGroup, Number(accountId), groupId);

export const moveSourceToGroup = (source: ChannelSource, groupId: string) =>
    invokeBackend(MoveSourceToGroup, source.peerKind, source.peerId, Number(source.accountId), source.generation, groupId);
