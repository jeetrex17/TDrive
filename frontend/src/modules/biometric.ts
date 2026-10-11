import { cancelBiometricUnlock } from '../api';

let pendingCancellation: Promise<void> | null = null;

// A closed dialog stays closed immediately. Native work for a new dialog must
// wait for the old cancellation to finish, or its prompt could be canceled by
// the delayed bridge call belonging to the previous dialog.
export function cancelBiometricAuthentication(): void {
    const cancel = async () => {
        try {
            const result = await cancelBiometricUnlock();
            if (!result.ok) console.warn('Could not cancel biometric authentication.');
        } catch {
            console.warn('Could not cancel biometric authentication.');
        }
    };
    const cancellation = pendingCancellation ? pendingCancellation.then(cancel) : cancel();
    pendingCancellation = cancellation;
    void cancellation.then(() => {
        if (pendingCancellation === cancellation) pendingCancellation = null;
    });
}

export async function waitForBiometricCancellation(): Promise<void> {
    while (pendingCancellation) await pendingCancellation;
}
