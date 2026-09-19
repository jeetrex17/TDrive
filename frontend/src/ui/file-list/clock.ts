import { readable } from 'svelte/store';
import { isMobilePlatform } from '../../api';

const MINUTE_MS = 60_000;

/**
 * Now, to the minute, for the phone row's spoken ages.
 *
 * Those labels are computed while the row draws, so without a clock to depend
 * on they freeze at that moment: a file uploaded while its folder is open says
 * "Just now" for as long as the folder stays open, and an hour-old file keeps
 * insisting it is three minutes old. A minute is the finest step any of the
 * labels take, so it is the whole tick needed.
 *
 * Desktop rows print a calendar date and never read this, so the interval is
 * never started there -- waking the list every minute to redraw dates that
 * cannot change would cost more than it fixes.
 */
export const minuteTick = readable(Date.now(), (set) => {
    if (!isMobilePlatform()) return;
    const timer = setInterval(() => set(Date.now()), MINUTE_MS);
    return () => clearInterval(timer);
});
