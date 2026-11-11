import {
    GetFileSortPreference,
    SetFileSortPreference,
} from '../../bindings/TDrive/internal/app/app';
import { invokeBackend } from './gateway';

export function loadFileSortPreference() {
    return invokeBackend(GetFileSortPreference);
}

export function saveFileSortPreference(preference: { key: string; direction: string }) {
    return invokeBackend(SetFileSortPreference, preference);
}
