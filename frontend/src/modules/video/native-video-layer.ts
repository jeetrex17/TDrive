/**
 * macOS embeds libmpv as a sibling view *below* the transparent WebView so the
 * HTML transport controls stay interactive on top of the picture. That only
 * works while nothing in the document paints over the video: the marker class
 * below drives the CSS that clears the page canvas.
 *
 * It has to land on <html> as well as <body>. A background on <body> alone
 * propagates to the page canvas, so clearing <body> used to be enough - but the
 * moment <html> carries a background of its own (the themed backdrop does),
 * propagation stops and <html> paints an opaque rectangle over the video.
 */
export const NATIVE_VIDEO_LAYER_CLASS = "native-video-active";

/** Reveals (or re-covers) the native renderer sitting behind the WebView. */
export function setNativeVideoLayerActive(target: Document | undefined, active: boolean): void {
    target?.documentElement?.classList.toggle(NATIVE_VIDEO_LAYER_CLASS, active);
    target?.body?.classList.toggle(NATIVE_VIDEO_LAYER_CLASS, active);
}
