import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
    getStatus: vi.fn(),
    prompt: vi.fn(),
}));

vi.mock('../api', () => ({ getEncryptionStatus: mocks.getStatus }));
vi.mock('./modals/encryption-password', () => ({ openEncryptionPasswordModal: mocks.prompt }));
vi.mock('../ui/chrome/profile-store', () => ({ encryptionEntryVisible: { set: vi.fn() } }));

import { state } from '../state';
import { accessEncryptedResource } from './encryption';

describe('encrypted resource access', () => {
    beforeEach(() => {
        vi.clearAllMocks();
        state.encryption = {
            available: true,
            passwordSet: true,
            passwordRemembered: false,
            hint: '',
            loaded: true,
        };
    });

    it('prompts before opening a known encrypted file', async () => {
        const open = vi.fn(async () => 'opened');
        mocks.prompt.mockResolvedValue(true);

        await expect(accessEncryptedResource(true, open)).resolves.toBe('opened');

        expect(mocks.prompt).toHaveBeenCalledOnce();
        expect(open).toHaveBeenCalledOnce();
    });

    it('prompts and retries when stale metadata omitted encryption', async () => {
        const open = vi.fn()
            .mockRejectedValueOnce(new Error('media: encryption key is unavailable: encryption password required'))
            .mockResolvedValueOnce('opened');
        mocks.prompt.mockResolvedValue(true);

        await expect(accessEncryptedResource(false, open)).resolves.toBe('opened');

        expect(mocks.prompt).toHaveBeenCalledOnce();
        expect(open).toHaveBeenCalledTimes(2);
    });

    it('does not open when password entry is canceled', async () => {
        const open = vi.fn(async () => 'opened');
        mocks.prompt.mockResolvedValue(false);

        await expect(accessEncryptedResource(true, open)).resolves.toBeNull();

        expect(open).not.toHaveBeenCalled();
    });
});
