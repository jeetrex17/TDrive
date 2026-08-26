import { describe, expect, it } from 'vitest';
import { eventOccurredWithin } from './event-path';

describe('event path containment', () => {
    it('keeps an interaction inside when reactive DOM updates detach its target', () => {
        const container = document.createElement('div');
        const target = document.createElement('button');
        container.appendChild(target);
        target.remove();

        const event = {
            target,
            composedPath: () => [target, container, document, window],
        } as unknown as Event;

        expect(container.contains(target)).toBe(false);
        expect(eventOccurredWithin(event, container)).toBe(true);
    });

    it('falls back to live containment when composedPath is unavailable', () => {
        const container = document.createElement('div');
        const target = document.createElement('button');
        container.appendChild(target);
        const event = { target } as unknown as Event;

        expect(eventOccurredWithin(event, container)).toBe(true);
        expect(eventOccurredWithin(event, null)).toBe(false);
    });
});
