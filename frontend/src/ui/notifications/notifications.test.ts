import { describe, expect, it } from 'vitest';
import { render } from 'svelte/server';
import EventRow from './EventRow.svelte';
import type { NoticeEvent } from './notif-store';

describe('EventRow server rendering', () => {
    it('escapes untrusted notification titles', () => {
        const event: NoticeEvent = {
            kind: 'event',
            id: 'e2',
            level: 'info',
            title: '<img src=x>',
            body: '',
            ts: 0,
        };

        const { body } = render(EventRow, { props: { event } });

        expect(body).not.toContain('<img src=x>');
    });
});
