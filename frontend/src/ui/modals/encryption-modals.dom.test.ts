import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, tick, unmount } from 'svelte';
import EncryptionPasswordModal from './EncryptionPasswordModal.svelte';
import EncryptionSettingsModal from './EncryptionSettingsModal.svelte';
import EncryptionSetupModal from './EncryptionSetupModal.svelte';
import { encryptionPasswordModal } from './encryption-password-modal-store';
import { encryptionSettingsModal } from './encryption-settings-modal-store';
import { encryptionSetupModal } from './encryption-setup-modal-store';

type MountedComponent = Record<string, unknown>;

const mounted: MountedComponent[] = [];
const hosts: HTMLElement[] = [];

function deferred(): { promise: Promise<void>; resolve: () => void } {
    let resolve = () => {};
    const promise = new Promise<void>((done) => {
        resolve = done;
    });
    return { promise, resolve };
}

function createHost(id: string): HTMLElement {
    const host = document.createElement('div');
    host.id = id;
    document.body.appendChild(host);
    hosts.push(host);
    return host;
}

function input(host: HTMLElement, selector: string): HTMLInputElement {
    const element = host.querySelector<HTMLInputElement>(selector);
    if (!element) throw new Error(`Expected ${selector} to be rendered`);
    return element;
}

function enter(host: HTMLElement, selector: string, value: string): HTMLInputElement {
    const element = input(host, selector);
    element.value = value;
    element.dispatchEvent(new Event('input', { bubbles: true }));
    flushSync();
    return element;
}

function click(host: HTMLElement, selector: string): void {
    const element = host.querySelector<HTMLButtonElement>(selector);
    if (!element) throw new Error(`Expected ${selector} to be rendered`);
    element.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    flushSync();
}

async function settleSubmission(): Promise<void> {
    for (let i = 0; i < 2; i += 1) {
        await Promise.resolve();
        await tick();
        flushSync();
    }
}

afterEach(async () => {
    encryptionPasswordModal.close();
    encryptionSetupModal.close();
    encryptionSettingsModal.close();
    flushSync();

    for (const component of mounted.splice(0)) {
        await unmount(component);
    }
    for (const host of hosts.splice(0)) {
        host.remove();
    }
});

describe('EncryptionPasswordModal secret lifecycle', () => {
    it('clears the password before cancel closes the form and on reopen', () => {
        const host = createHost('encryption-password-modal');
        let passwordAtCancel = 'not-called';
        mounted.push(mount(EncryptionPasswordModal, {
            target: host,
            props: {
                onCancel: () => {
                    passwordAtCancel = input(host, '#encryption-password-input').value;
                    encryptionPasswordModal.close();
                },
                onSubmit: vi.fn(),
            },
        }));

        encryptionPasswordModal.open({ hint: '' });
        flushSync();
        const exitedInput = enter(host, '#encryption-password-input', 'cancel-secret');
        click(host, '#encryption-password-cancel');

        expect(passwordAtCancel).toBe('');
        expect(exitedInput.value).toBe('');

        encryptionPasswordModal.open({ hint: '' });
        flushSync();
        expect(input(host, '#encryption-password-input').value).toBe('');
    });

    it('clears the password after a successful submit and on reopen', async () => {
        const host = createHost('encryption-password-modal');
        const completion = deferred();
        const onSubmit = vi.fn(async (password: string) => {
            expect(password).toBe('success-secret');
            await completion.promise;
            encryptionPasswordModal.close();
        });
        mounted.push(mount(EncryptionPasswordModal, {
            target: host,
            props: { onCancel: vi.fn(), onSubmit },
        }));

        encryptionPasswordModal.open({ hint: '' });
        flushSync();
        const exitedInput = enter(host, '#encryption-password-input', 'success-secret');
        click(host, '#encryption-password-confirm');

        expect(onSubmit).toHaveBeenCalledOnce();
        expect(exitedInput.value).toBe('');

        completion.resolve();
        await settleSubmission();
        encryptionPasswordModal.open({ hint: '' });
        flushSync();
        expect(input(host, '#encryption-password-input').value).toBe('');
    });

    it('clears the password when a handled submit error keeps the form open', async () => {
        const host = createHost('encryption-password-modal');
        mounted.push(mount(EncryptionPasswordModal, {
            target: host,
            props: {
                onCancel: vi.fn(),
                onSubmit: async () => encryptionPasswordModal.setError('Wrong password.'),
            },
        }));

        encryptionPasswordModal.open({ hint: '' });
        flushSync();
        enter(host, '#encryption-password-input', 'error-secret');
        click(host, '#encryption-password-confirm');
        await settleSubmission();

        expect(input(host, '#encryption-password-input').value).toBe('');
        expect(host.querySelector('#encryption-password-error')?.textContent).toContain('Wrong password.');

        encryptionPasswordModal.close();
        flushSync();
        encryptionPasswordModal.open({ hint: '' });
        flushSync();
        expect(input(host, '#encryption-password-input').value).toBe('');
    });

    it('does not let an old submit clear a newly reopened password form', async () => {
        const host = createHost('encryption-password-modal');
        const completion = deferred();
        mounted.push(mount(EncryptionPasswordModal, {
            target: host,
            props: { onCancel: vi.fn(), onSubmit: () => completion.promise },
        }));

        encryptionPasswordModal.open({ hint: '' });
        flushSync();
        enter(host, '#encryption-password-input', 'old-secret');
        click(host, '#encryption-password-confirm');
        encryptionPasswordModal.close();
        flushSync();
        encryptionPasswordModal.open({ hint: '' });
        flushSync();
        enter(host, '#encryption-password-input', 'new-secret');

        completion.resolve();
        await settleSubmission();

        expect(input(host, '#encryption-password-input').value).toBe('new-secret');
    });
});

describe('EncryptionSetupModal secret lifecycle', () => {
    it('clears both passwords before cancel closes the form and on reopen', () => {
        const host = createHost('encryption-setup-modal');
        let passwordsAtCancel: string[] = [];
        mounted.push(mount(EncryptionSetupModal, {
            target: host,
            props: {
                onCancel: () => {
                    passwordsAtCancel = [
                        input(host, '#encryption-setup-password').value,
                        input(host, '#encryption-setup-password-confirm').value,
                    ];
                    encryptionSetupModal.close();
                },
                onSubmit: vi.fn(),
            },
        }));

        encryptionSetupModal.open(null);
        flushSync();
        const exitedPassword = enter(host, '#encryption-setup-password', 'cancel-secret');
        const exitedConfirm = enter(host, '#encryption-setup-password-confirm', 'cancel-secret');
        click(host, '#encryption-setup-cancel');

        expect(passwordsAtCancel).toEqual(['', '']);
        expect(exitedPassword.value).toBe('');
        expect(exitedConfirm.value).toBe('');

        encryptionSetupModal.open(null);
        flushSync();
        expect(input(host, '#encryption-setup-password').value).toBe('');
        expect(input(host, '#encryption-setup-password-confirm').value).toBe('');
    });

    it('clears both passwords after a successful submit and on reopen', async () => {
        const host = createHost('encryption-setup-modal');
        const completion = deferred();
        const onSubmit = vi.fn(async (password: string, confirmPassword: string) => {
            expect([password, confirmPassword]).toEqual(['success-secret', 'success-secret']);
            await completion.promise;
            encryptionSetupModal.close();
        });
        mounted.push(mount(EncryptionSetupModal, {
            target: host,
            props: { onCancel: vi.fn(), onSubmit },
        }));

        encryptionSetupModal.open(null);
        flushSync();
        const exitedPassword = enter(host, '#encryption-setup-password', 'success-secret');
        const exitedConfirm = enter(host, '#encryption-setup-password-confirm', 'success-secret');
        click(host, '#encryption-setup-confirm');

        expect(onSubmit).toHaveBeenCalledOnce();
        expect(exitedPassword.value).toBe('');
        expect(exitedConfirm.value).toBe('');

        completion.resolve();
        await settleSubmission();
        encryptionSetupModal.open(null);
        flushSync();
        expect(input(host, '#encryption-setup-password').value).toBe('');
        expect(input(host, '#encryption-setup-password-confirm').value).toBe('');
    });

    it('clears both passwords when a handled submit error keeps the form open', async () => {
        const host = createHost('encryption-setup-modal');
        mounted.push(mount(EncryptionSetupModal, {
            target: host,
            props: {
                onCancel: vi.fn(),
                onSubmit: async () => encryptionSetupModal.setError('Setup failed.'),
            },
        }));

        encryptionSetupModal.open(null);
        flushSync();
        enter(host, '#encryption-setup-password', 'error-secret');
        enter(host, '#encryption-setup-password-confirm', 'error-secret');
        click(host, '#encryption-setup-confirm');
        await settleSubmission();

        expect(input(host, '#encryption-setup-password').value).toBe('');
        expect(input(host, '#encryption-setup-password-confirm').value).toBe('');
        expect(host.querySelector('#encryption-setup-error')?.textContent).toContain('Setup failed.');

        encryptionSetupModal.close();
        flushSync();
        encryptionSetupModal.open(null);
        flushSync();
        expect(input(host, '#encryption-setup-password').value).toBe('');
        expect(input(host, '#encryption-setup-password-confirm').value).toBe('');
    });

    it('does not let an old submit clear a newly reopened setup form', async () => {
        const host = createHost('encryption-setup-modal');
        const completion = deferred();
        mounted.push(mount(EncryptionSetupModal, {
            target: host,
            props: { onCancel: vi.fn(), onSubmit: () => completion.promise },
        }));

        encryptionSetupModal.open(null);
        flushSync();
        enter(host, '#encryption-setup-password', 'old-secret');
        enter(host, '#encryption-setup-password-confirm', 'old-secret');
        click(host, '#encryption-setup-confirm');
        encryptionSetupModal.close();
        flushSync();
        encryptionSetupModal.open(null);
        flushSync();
        enter(host, '#encryption-setup-password', 'new-secret');
        enter(host, '#encryption-setup-password-confirm', 'new-secret');

        completion.resolve();
        await settleSubmission();

        expect(input(host, '#encryption-setup-password').value).toBe('new-secret');
        expect(input(host, '#encryption-setup-password-confirm').value).toBe('new-secret');
    });
});

describe('EncryptionSettingsModal secret lifecycle', () => {
    const selectors = [
        '#encryption-current-password',
        '#encryption-new-password',
        '#encryption-new-password-confirm',
    ];

    function values(host: HTMLElement): string[] {
        return selectors.map((selector) => input(host, selector).value);
    }

    function enterPasswords(host: HTMLElement, value: string): HTMLInputElement[] {
        return selectors.map((selector) => enter(host, selector, value));
    }

    it('shows distinct icons for hidden and revealed passwords', () => {
        const host = createHost('encryption-settings-modal');
        mounted.push(mount(EncryptionSettingsModal, {
            target: host,
            props: { onCancel: vi.fn(), onSubmit: vi.fn() },
        }));

        encryptionSettingsModal.open({ hint: '' });
        flushSync();

        for (const button of host.querySelectorAll<HTMLButtonElement>('.reveal-on-hold')) {
            expect(button.querySelector('.icon-eye')).not.toBeNull();
            expect(button.querySelector('.icon-eye-off')).not.toBeNull();
        }
    });

    it('clears all passwords before cancel closes the form and on reopen', () => {
        const host = createHost('encryption-settings-modal');
        let passwordsAtCancel: string[] = [];
        mounted.push(mount(EncryptionSettingsModal, {
            target: host,
            props: {
                onCancel: () => {
                    passwordsAtCancel = values(host);
                    encryptionSettingsModal.close();
                },
                onSubmit: vi.fn(),
            },
        }));

        encryptionSettingsModal.open({ hint: 'keep this hint' });
        flushSync();
        const exitedInputs = enterPasswords(host, 'cancel-secret');
        click(host, '#encryption-settings-cancel');

        expect(passwordsAtCancel).toEqual(['', '', '']);
        expect(exitedInputs.map((element) => element.value)).toEqual(['', '', '']);

        encryptionSettingsModal.open({ hint: 'keep this hint' });
        flushSync();
        expect(values(host)).toEqual(['', '', '']);
    });

    it('clears all passwords after a successful submit and on reopen', async () => {
        const host = createHost('encryption-settings-modal');
        const completion = deferred();
        const onSubmit = vi.fn(async (current: string, next: string, confirm: string) => {
            expect([current, next, confirm]).toEqual(['success-secret', 'success-secret', 'success-secret']);
            encryptionSettingsModal.setBusy(true);
            await completion.promise;
            encryptionSettingsModal.close();
        });
        mounted.push(mount(EncryptionSettingsModal, {
            target: host,
            props: { onCancel: vi.fn(), onSubmit },
        }));

        encryptionSettingsModal.open({ hint: 'keep this hint' });
        flushSync();
        const exitedInputs = enterPasswords(host, 'success-secret');
        click(host, '#encryption-settings-confirm');

        expect(onSubmit).toHaveBeenCalledOnce();
        expect(exitedInputs.map((element) => element.value)).toEqual(['', '', '']);
        for (const button of host.querySelectorAll<HTMLButtonElement>('.reveal-on-hold')) {
            expect(button.disabled).toBe(true);
        }

        completion.resolve();
        await settleSubmission();
        encryptionSettingsModal.open({ hint: 'keep this hint' });
        flushSync();
        expect(values(host)).toEqual(['', '', '']);
    });

    it('clears all passwords when a handled submit error keeps the form open', async () => {
        const host = createHost('encryption-settings-modal');
        mounted.push(mount(EncryptionSettingsModal, {
            target: host,
            props: {
                onCancel: vi.fn(),
                onSubmit: async () => encryptionSettingsModal.setError('Change failed.'),
            },
        }));

        encryptionSettingsModal.open({ hint: 'keep this hint' });
        flushSync();
        enterPasswords(host, 'error-secret');
        click(host, '#encryption-settings-confirm');
        await settleSubmission();

        expect(values(host)).toEqual(['', '', '']);
        expect(host.querySelector('#encryption-settings-error')?.textContent).toContain('Change failed.');

        encryptionSettingsModal.close();
        flushSync();
        encryptionSettingsModal.open({ hint: 'keep this hint' });
        flushSync();
        expect(values(host)).toEqual(['', '', '']);
    });

    it('does not let an old submit clear a newly reopened settings form', async () => {
        const host = createHost('encryption-settings-modal');
        const completion = deferred();
        mounted.push(mount(EncryptionSettingsModal, {
            target: host,
            props: { onCancel: vi.fn(), onSubmit: () => completion.promise },
        }));

        encryptionSettingsModal.open({ hint: 'keep this hint' });
        flushSync();
        enterPasswords(host, 'old-secret');
        click(host, '#encryption-settings-confirm');
        encryptionSettingsModal.close();
        flushSync();
        encryptionSettingsModal.open({ hint: 'keep this hint' });
        flushSync();
        enterPasswords(host, 'new-secret');

        completion.resolve();
        await settleSubmission();

        expect(values(host)).toEqual(['new-secret', 'new-secret', 'new-secret']);
    });
});
