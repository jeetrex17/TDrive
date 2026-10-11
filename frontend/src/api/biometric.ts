import {
    BiometricStatus as rawBiometricStatus,
    EnableBiometricUnlock as rawEnableBiometricUnlock,
    UseBiometricUnlock as rawUseBiometricUnlock,
    DisableBiometricUnlock as rawDisableBiometricUnlock,
    CancelBiometricUnlock as rawCancelBiometricUnlock,
} from '../../bindings/TDrive/internal/app/encryptionservice';
import type { BiometricKind, BiometricStatusView, OperationResult } from '../types';
import { invokeBackend } from './gateway';
import { normalizeOperationResult } from './operation';
import { asRecord } from './shared';

const KINDS = new Set(['touch_id', 'face_id', 'windows_hello', 'biometric']);
const REASONS = new Set(['', 'unsupported', 'not_enrolled', 'locked_out', 'unavailable', 'signing_required', 'invalidated']);

export function normalizeBiometricStatus(value: unknown): BiometricStatusView {
    const raw = asRecord(value);
    return {
        available: raw.available === true,
        enrolled: raw.enrolled === true,
        kind: typeof raw.kind === 'string' && KINDS.has(raw.kind) ? raw.kind as BiometricKind : 'biometric',
        reason: typeof raw.reason === 'string' && REASONS.has(raw.reason) ? raw.reason : 'unavailable',
    };
}

export async function getBiometricStatus(): Promise<BiometricStatusView> {
    return normalizeBiometricStatus(await invokeBackend(rawBiometricStatus));
}

export async function enableBiometricUnlock(password: string): Promise<OperationResult> {
    return normalizeOperationResult(await invokeBackend(rawEnableBiometricUnlock, password), 'Biometric enrollment failed');
}

export async function useBiometricUnlock(): Promise<OperationResult> {
    return normalizeOperationResult(await invokeBackend(rawUseBiometricUnlock), 'Biometric unlock failed');
}

export async function disableBiometricUnlock(): Promise<OperationResult> {
    return normalizeOperationResult(await invokeBackend(rawDisableBiometricUnlock), 'Could not disable biometric unlock');
}

export async function cancelBiometricUnlock(): Promise<OperationResult> {
    return normalizeOperationResult(await invokeBackend(rawCancelBiometricUnlock), 'Could not cancel biometric unlock');
}
