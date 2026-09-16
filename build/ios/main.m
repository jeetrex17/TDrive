//go:build ios
// Minimal bootstrap: delegate comes from Go archive (WailsAppDelegate)
#import <UIKit/UIKit.h>
#import <WebKit/WebKit.h>
#import <objc/runtime.h>
#include <stdio.h>

// Wails lays its WKWebView out inside the safe area. On a notched phone held
// sideways that leaves a 59 point strip of bare, unpainted view down each edge
// and hands the page a 756 point viewport on an 874 point screen, which is both
// a visible band of nothing and a width that trips the layout's own compact
// breakpoint.
//
// The page does not want that. It asks for viewport-fit=cover and reserves the
// edges itself in CSS, per surface: a video fills the glass while its controls
// stay clear of the island and the home indicator. So the web view is put back
// to the full screen here and the insets are left to env(safe-area-inset-*),
// which is what they are for.
//
// A host using Wails' own native tab bar is left alone: that bar is a real view
// occupying real space, and the web view has to stop above it.
@interface WailsViewController : UIViewController
@property (nonatomic, strong) WKWebView *webView;
@property (nonatomic, strong) UITabBar *tabBar;
@end

static void (*tdriveInsetLayout)(id, SEL) = NULL;

static void tdriveFullBleedLayout(id self, SEL _cmd) {
    if (tdriveInsetLayout != NULL) {
        tdriveInsetLayout(self, _cmd);
    }
    WailsViewController *controller = (WailsViewController *)self;
    if (controller.tabBar != nil && !controller.tabBar.isHidden) {
        return;
    }
    if (controller.webView != nil) {
        controller.webView.frame = controller.view.bounds;
    }
}

// Installed by swizzle rather than by a category method, because a category
// that redefines an existing method has no defined winner. If Wails ever stops
// insetting, or renames the controller, this finds nothing and does nothing.
@implementation WailsViewController (TDriveFullBleed)
+ (void)load {
    Class controller = NSClassFromString(@"WailsViewController");
    if (controller == Nil) {
        return;
    }
    Method layout = class_getInstanceMethod(controller, @selector(viewDidLayoutSubviews));
    if (layout == NULL) {
        return;
    }
    tdriveInsetLayout = (void (*)(id, SEL))method_getImplementation(layout);
    method_setImplementation(layout, (IMP)tdriveFullBleedLayout);
}
@end

int main(int argc, char * argv[]) {
    @autoreleasepool {
        // Disable buffering so stdout/stderr from Go log.Printf flush immediately
        setvbuf(stdout, NULL, _IONBF, 0);
        setvbuf(stderr, NULL, _IONBF, 0);

        // Call UIApplicationMain IMMEDIATELY and start NOTHING else here. Do not
        // start the Go runtime yet: starting it concurrently with UIApplicationMain
        // intermittently corrupts the FrontBoard launch handshake on a physical
        // device, so the app delegate's didFinishLaunchingWithOptions never fires
        // (blank cold launch / 0x8BADF00D). Instead, the WailsAppDelegate (provided
        // by the Go archive) starts the Go runtime itself from
        // didFinishLaunchingWithOptions - i.e. only AFTER UIKit has delivered the
        // launch - so the runtime never races the launch handshake.
        return UIApplicationMain(argc, argv, nil, @"WailsAppDelegate");
    }
}
