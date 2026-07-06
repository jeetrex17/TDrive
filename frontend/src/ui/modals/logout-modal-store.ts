import { createModalController } from './modal-store';

export type LogoutMode = 'soft' | 'full';

export const logoutModal = createModalController<null>();
