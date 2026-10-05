/**
 * The one place the app asks for haptic feedback.
 *
 * Every call names what happened, not how it should feel: the OS owns the
 * waveform, and it already honours the user's system haptics setting, so a
 * phone with feedback turned off stays silent without us checking anything.
 *
 * There is deliberately no iOS/Android branch here. Both hosts implement the
 * same semantic vocabulary behind `application.Mobile.Haptic`, so the single
 * `Haptic` binding covers both and Android stops reaching for a raw
 * millisecond vibrate that it would have had to guess a weight for.
 *
 * The rule for adding a call site: feedback marks a moment the user caused and
 * could not otherwise feel. A press that crosses into a new mode, a threshold
 * arming, a value actually changing, an operation actually finishing. Not every
 * tap -- a tap already has a visible result, and a buzz per tap is noise that
 * trains people to turn haptics off.
 */

import { playHaptic } from '../../api';

/** A long press crossing into selection, or a pull-to-refresh arming. */
export function hapticPress(): void {
    playHaptic('impact-light');
}
