import { beforeEach, describe, expect, it } from 'vitest';
import { idleTransferActivity, setTransferDirectionActive, state } from './state';

describe('transfer activity', () => {
    beforeEach(() => {
        state.transferActivity = idleTransferActivity;
    });

    it('keeps download active when an overlapping upload completes first', () => {
        setTransferDirectionActive('upload', true);
        setTransferDirectionActive('download', true);
        setTransferDirectionActive('upload', false);

        expect(state.transferActivity).toEqual({ upload: false, download: true });
    });

    it('keeps upload active when an overlapping download completes first', () => {
        setTransferDirectionActive('download', true);
        setTransferDirectionActive('upload', true);
        setTransferDirectionActive('download', false);

        expect(state.transferActivity).toEqual({ upload: true, download: false });
    });
});
