//go:build darwin

package nativeplayer

/*
#cgo darwin CFLAGS: -x objective-c -fblocks -DGL_SILENCE_DEPRECATION
#cgo darwin LDFLAGS: -framework Cocoa -framework QuartzCore -framework OpenGL
#cgo darwin pkg-config: mpv

#import <Cocoa/Cocoa.h>
#import <OpenGL/gl3.h>
#import <QuartzCore/QuartzCore.h>
#import <dispatch/dispatch.h>
#import <mpv/client.h>
#import <mpv/render.h>
#import <mpv/render_gl.h>

static void *tdrive_gl_get_proc_address(void *ctx, const char *name) {
    CFStringRef symbol = CFStringCreateWithCString(kCFAllocatorDefault, name, kCFStringEncodingASCII);
    void *addr = CFBundleGetFunctionPointerForName(CFBundleGetBundleWithIdentifier(CFSTR("com.apple.opengl")), symbol);
    CFRelease(symbol);
    return addr;
}

@interface TDriveMPVView : NSOpenGLView {
    mpv_handle *_mpv;
    mpv_render_context *_render;
    BOOL _closed;
}
- (BOOL)startWithURL:(NSString *)url;
- (void)shutdown;
- (int)sendCommand:(int)argc argv:(const char **)argv;
@end

static void tdrive_mpv_render_update(void *ctx) {
    TDriveMPVView *view = (TDriveMPVView *)ctx;
    dispatch_async(dispatch_get_main_queue(), ^{
        [view setNeedsDisplay:YES];
    });
}

@implementation TDriveMPVView

+ (NSOpenGLPixelFormat *)tdrivePixelFormat {
    NSOpenGLPixelFormatAttribute attrs[] = {
        NSOpenGLPFAOpenGLProfile, NSOpenGLProfileVersion3_2Core,
        NSOpenGLPFAColorSize, 24,
        NSOpenGLPFAAlphaSize, 8,
        NSOpenGLPFADoubleBuffer,
        NSOpenGLPFAAccelerated,
        0
    };
    return [[[NSOpenGLPixelFormat alloc] initWithAttributes:attrs] autorelease];
}

- (instancetype)initWithFrame:(NSRect)frameRect {
    self = [super initWithFrame:frameRect pixelFormat:[TDriveMPVView tdrivePixelFormat]];
    if (self) {
        [self setWantsBestResolutionOpenGLSurface:YES];
        [self setWantsLayer:YES];
        [[self layer] setBackgroundColor:[[NSColor blackColor] CGColor]];
    }
    return self;
}

- (BOOL)startWithURL:(NSString *)url {
    _mpv = mpv_create();
    if (_mpv == NULL) {
        return NO;
    }

	mpv_set_option_string(_mpv, "config", "no");
	mpv_set_option_string(_mpv, "terminal", "no");
	mpv_set_option_string(_mpv, "msg-level", "all=warn");
	mpv_set_option_string(_mpv, "vo", "libmpv");
	mpv_set_option_string(_mpv, "ytdl", "no");
	mpv_set_option_string(_mpv, "hwdec", "auto-safe");
	mpv_set_option_string(_mpv, "osc", "yes");
	mpv_set_option_string(_mpv, "osd-bar", "yes");

    if (mpv_initialize(_mpv) < 0) {
        return NO;
    }

    [[self openGLContext] makeCurrentContext];
    mpv_opengl_init_params glInit = {
        .get_proc_address = tdrive_gl_get_proc_address,
        .get_proc_address_ctx = NULL,
    };
    mpv_render_param params[] = {
        { MPV_RENDER_PARAM_API_TYPE, (void *)MPV_RENDER_API_TYPE_OPENGL },
        { MPV_RENDER_PARAM_OPENGL_INIT_PARAMS, &glInit },
        { MPV_RENDER_PARAM_INVALID, NULL },
    };
    if (mpv_render_context_create(&_render, _mpv, params) < 0) {
        return NO;
    }
    mpv_render_context_set_update_callback(_render, tdrive_mpv_render_update, self);

    const char *cmd[] = { "loadfile", [url UTF8String], NULL };
    if (mpv_command_async(_mpv, 0, cmd) < 0) {
        return NO;
    }
    return YES;
}

- (void)drawRect:(NSRect)dirtyRect {
    if (_closed || _render == NULL) {
        [[NSColor blackColor] setFill];
        NSRectFill([self bounds]);
        return;
    }

    [[self openGLContext] makeCurrentContext];
    NSRect backing = [self convertRectToBacking:[self bounds]];
    int width = (int)MAX(1, backing.size.width);
    int height = (int)MAX(1, backing.size.height);
    glViewport(0, 0, width, height);
    glClearColor(0.0, 0.0, 0.0, 1.0);
    glClear(GL_COLOR_BUFFER_BIT);

    mpv_opengl_fbo fbo = { .fbo = 0, .w = width, .h = height, .internal_format = GL_RGBA8 };
    int flipY = 1;
    mpv_render_param params[] = {
        { MPV_RENDER_PARAM_OPENGL_FBO, &fbo },
        { MPV_RENDER_PARAM_FLIP_Y, &flipY },
        { MPV_RENDER_PARAM_INVALID, NULL },
    };
    mpv_render_context_render(_render, params);
    [[self openGLContext] flushBuffer];
}

- (void)reshape {
    [super reshape];
    [self setNeedsDisplay:YES];
}

- (int)sendCommand:(int)argc argv:(const char **)argv {
    if (_closed || _mpv == NULL || argc <= 0 || argv == NULL) {
        return 0;
    }
    return mpv_command_async(_mpv, 0, argv);
}

- (void)shutdown {
    if (_closed) {
        return;
    }
    _closed = YES;
    mpv_handle *mpv = _mpv;
    _mpv = NULL;
    if (_render != NULL) {
        [[self openGLContext] makeCurrentContext];
        mpv_render_context_set_update_callback(_render, NULL, NULL);
        mpv_render_context_free(_render);
        _render = NULL;
        [NSOpenGLContext clearCurrentContext];
    }
    if (mpv != NULL) {
        dispatch_async(dispatch_get_global_queue(DISPATCH_QUEUE_PRIORITY_DEFAULT, 0), ^{
            mpv_terminate_destroy(mpv);
        });
    }
}

- (void)dealloc {
    [self shutdown];
    [super dealloc];
}

@end

static NSWindow* tdrive_player_window(void) {
    NSWindow *window = [NSApp keyWindow];
    if (window != nil) {
        return window;
    }
    for (NSWindow *candidate in [NSApp windows]) {
        if ([candidate isVisible]) {
            return candidate;
        }
    }
    return nil;
}

static NSRect tdrive_player_frame(NSView *content, double x, double y, double w, double h) {
    NSRect bounds = [content bounds];
    double bottomY = bounds.size.height - y - h;
    return NSMakeRect(x, bottomY, w, h);
}

static void* tdrive_player_create_view(const char *rawURL, double x, double y, double w, double h) {
    if (rawURL == NULL) {
        return nil;
    }
    __block TDriveMPVView *view = nil;
    NSString *url = [NSString stringWithUTF8String:rawURL];
    void (^create)(void) = ^{
        NSWindow *window = tdrive_player_window();
        if (window == nil) {
            return;
        }
        NSView *content = [window contentView];
        if (content == nil) {
            return;
        }
        NSRect frame = tdrive_player_frame(content, x, y, w, h);
        view = [[TDriveMPVView alloc] initWithFrame:frame];
        if (view == nil || ![view startWithURL:url]) {
            [view release];
            view = nil;
            return;
        }
        [view setAutoresizingMask:0];
        [content addSubview:view positioned:NSWindowAbove relativeTo:nil];
    };
    if ([NSThread isMainThread]) {
        create();
    } else {
        dispatch_sync(dispatch_get_main_queue(), create);
    }
    return view;
}

static void tdrive_player_set_frame(void *ptr, double x, double y, double w, double h) {
    if (ptr == nil) {
        return;
    }
    TDriveMPVView *view = (TDriveMPVView *)ptr;
    void (^resize)(void) = ^{
        NSView *content = [view superview];
        if (content == nil) {
            return;
        }
        [view setFrame:tdrive_player_frame(content, x, y, w, h)];
        [view setNeedsDisplay:YES];
    };
    if ([NSThread isMainThread]) {
        resize();
    } else {
        dispatch_async(dispatch_get_main_queue(), resize);
    }
}

static int tdrive_player_command(void *ptr, int argc, const char **argv) {
    if (ptr == nil) {
        return 0;
    }
    __block int result = 0;
    TDriveMPVView *view = (TDriveMPVView *)ptr;
    void (^send)(void) = ^{
        result = [view sendCommand:argc argv:argv];
    };
    if ([NSThread isMainThread]) {
        send();
    } else {
        dispatch_sync(dispatch_get_main_queue(), send);
    }
    return result;
}

static void tdrive_player_destroy_view(void *ptr) {
    if (ptr == nil) {
        return;
    }
    TDriveMPVView *view = (TDriveMPVView *)ptr;
    void (^destroy)(void) = ^{
        [view shutdown];
        [view removeFromSuperview];
        [view release];
    };
    if ([NSThread isMainThread]) {
        destroy();
    } else {
        dispatch_sync(dispatch_get_main_queue(), destroy);
    }
}
*/
import "C"

import (
	"context"
	"fmt"
	"sync"
	"unsafe"
)

type Player struct {
	mu     sync.Mutex
	view   unsafe.Pointer
	closed bool
}

func Start(ctx context.Context, url string, rect Rect) (*Player, error) {
	if !rect.Valid() {
		return nil, fmt.Errorf("native player: invalid view rect")
	}
	rawURL := C.CString(url)
	defer C.free(unsafe.Pointer(rawURL))
	view := C.tdrive_player_create_view(rawURL, C.double(rect.X), C.double(rect.Y), C.double(rect.Width), C.double(rect.Height))
	if view == nil {
		return nil, fmt.Errorf("native player: could not start libmpv view")
	}
	return &Player{view: view}, nil
}

func (p *Player) Resize(rect Rect) error {
	if p == nil {
		return nil
	}
	if !rect.Valid() {
		return fmt.Errorf("native player: invalid view rect")
	}
	p.mu.Lock()
	view := p.view
	closed := p.closed
	p.mu.Unlock()
	if closed || view == nil {
		return nil
	}
	C.tdrive_player_set_frame(view, C.double(rect.X), C.double(rect.Y), C.double(rect.Width), C.double(rect.Height))
	return nil
}

func (p *Player) Command(command ...string) error {
	if p == nil || len(command) == 0 {
		return nil
	}
	p.mu.Lock()
	view := p.view
	closed := p.closed
	p.mu.Unlock()
	if closed || view == nil {
		return nil
	}

	cStrings := make([]*C.char, len(command)+1)
	for i, part := range command {
		cStrings[i] = C.CString(part)
	}
	defer func() {
		for _, cstr := range cStrings {
			if cstr != nil {
				C.free(unsafe.Pointer(cstr))
			}
		}
	}()
	if rc := C.tdrive_player_command(view, C.int(len(command)), (**C.char)(unsafe.Pointer(&cStrings[0]))); rc < 0 {
		return fmt.Errorf("native player: libmpv command failed: %d", int(rc))
	}
	return nil
}

func (p *Player) Close() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	view := p.view
	p.view = nil
	p.mu.Unlock()
	if view != nil {
		C.tdrive_player_destroy_view(view)
	}
	return nil
}
