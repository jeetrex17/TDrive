//go:build darwin

package nativeplayer

/*
#cgo darwin CFLAGS: -x objective-c -fblocks -DGL_SILENCE_DEPRECATION
#cgo darwin LDFLAGS: -framework Cocoa -framework QuartzCore -framework OpenGL
#cgo darwin pkg-config: mpv

#import <Cocoa/Cocoa.h>
#import <math.h>
#import <OpenGL/gl3.h>
#import <QuartzCore/QuartzCore.h>
#import <dispatch/dispatch.h>
#import <mpv/client.h>
#import <mpv/render.h>
#import <mpv/render_gl.h>
#import <stdint.h>
#import <string.h>

static void *tdrive_gl_get_proc_address(void *ctx, const char *name) {
    CFStringRef symbol = CFStringCreateWithCString(kCFAllocatorDefault, name, kCFStringEncodingASCII);
    void *addr = CFBundleGetFunctionPointerForName(CFBundleGetBundleWithIdentifier(CFSTR("com.apple.opengl")), symbol);
    CFRelease(symbol);
    return addr;
}

typedef struct {
    double time_pos;
    double duration;
    double volume;
    double speed;
    double cache_duration;
    int64_t cache_buffering_state;
    int paused;
    int muted;
    int paused_for_cache;
    int eof_reached;
    int has_time_pos;
    int has_duration;
    int has_cache_duration;
} tdrive_player_state;

static int tdrive_mpv_get_double(mpv_handle *mpv, const char *name, double *out) {
    double value = 0;
    if (mpv_get_property(mpv, name, MPV_FORMAT_DOUBLE, &value) < 0 || !isfinite(value)) {
        return 0;
    }
    *out = value;
    return 1;
}

static int tdrive_mpv_get_flag(mpv_handle *mpv, const char *name) {
    int value = 0;
    if (mpv_get_property(mpv, name, MPV_FORMAT_FLAG, &value) < 0) {
        return 0;
    }
    return value != 0;
}

static int64_t tdrive_mpv_get_int64(mpv_handle *mpv, const char *name) {
    int64_t value = 0;
    if (mpv_get_property(mpv, name, MPV_FORMAT_INT64, &value) < 0) {
        return 0;
    }
    return value;
}

@interface TDriveMPVView : NSOpenGLView {
    mpv_handle *_mpv;
    mpv_render_context *_render;
    BOOL _closed;
}
- (BOOL)startWithURL:(NSString *)url htmlControls:(BOOL)htmlControls;
- (void)shutdown;
- (void)shutdownSynchronously;
- (int)sendCommand:(int)argc argv:(const char **)argv;
- (BOOL)snapshot:(tdrive_player_state *)state;
@end

static void tdrive_mpv_render_update(void *ctx) {
    TDriveMPVView *view = [(TDriveMPVView *)ctx retain];
    dispatch_async(dispatch_get_main_queue(), ^{
        [view setNeedsDisplay:YES];
        [view release];
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

- (BOOL)startWithURL:(NSString *)url htmlControls:(BOOL)htmlControls {
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
	mpv_set_option_string(_mpv, "cache", "yes");
	mpv_set_option_string(_mpv, "demuxer-readahead-secs", "20");
	mpv_set_option_string(_mpv, "demuxer-max-bytes", "67108864");
	mpv_set_option_string(_mpv, "demuxer-max-back-bytes", "33554432");
	mpv_set_option_string(_mpv, "osc", htmlControls ? "no" : "yes");
	mpv_set_option_string(_mpv, "osd-bar", htmlControls ? "no" : "yes");

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

- (BOOL)snapshot:(tdrive_player_state *)state {
    if (_closed || _mpv == NULL || state == NULL) {
        return NO;
    }

    memset(state, 0, sizeof(tdrive_player_state));
    state->paused = tdrive_mpv_get_flag(_mpv, "pause");
    state->muted = tdrive_mpv_get_flag(_mpv, "mute");
    state->paused_for_cache = tdrive_mpv_get_flag(_mpv, "paused-for-cache");
    state->eof_reached = tdrive_mpv_get_flag(_mpv, "eof-reached");
    state->cache_buffering_state = tdrive_mpv_get_int64(_mpv, "cache-buffering-state");
    state->volume = 100.0;
    state->speed = 1.0;
    tdrive_mpv_get_double(_mpv, "volume", &state->volume);
    tdrive_mpv_get_double(_mpv, "speed", &state->speed);
    state->has_time_pos = tdrive_mpv_get_double(_mpv, "time-pos", &state->time_pos);
    state->has_duration = tdrive_mpv_get_double(_mpv, "duration", &state->duration);
    state->has_cache_duration = tdrive_mpv_get_double(_mpv, "demuxer-cache-duration", &state->cache_duration);
    return YES;
}

- (void)shutdownWithAsyncTerminate:(BOOL)asyncTerminate {
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
        if (asyncTerminate) {
            TDriveMPVView *view = [self retain];
            dispatch_async(dispatch_get_global_queue(DISPATCH_QUEUE_PRIORITY_DEFAULT, 0), ^{
                mpv_terminate_destroy(mpv);
                dispatch_async(dispatch_get_main_queue(), ^{
                    [view release];
                });
            });
        } else {
            mpv_terminate_destroy(mpv);
        }
    }
}

- (void)shutdown {
    [self shutdownWithAsyncTerminate:YES];
}

- (void)shutdownSynchronously {
    [self shutdownWithAsyncTerminate:NO];
}

- (void)dealloc {
    [self shutdownSynchronously];
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

static void* tdrive_player_create_view(const char *rawURL, double x, double y, double w, double h, int htmlControls) {
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
        if (view == nil) {
            return;
        }
        if (![view startWithURL:url htmlControls:(htmlControls != 0)]) {
            [view shutdownSynchronously];
            [view release];
            view = nil;
            return;
        }
        [view setAutoresizingMask:0];
        [content addSubview:view positioned:(htmlControls ? NSWindowBelow : NSWindowAbove) relativeTo:nil];
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

static int tdrive_player_snapshot(void *ptr, tdrive_player_state *state) {
    if (ptr == nil || state == NULL) {
        return 0;
    }
    TDriveMPVView *view = (TDriveMPVView *)ptr;
    return [view snapshot:state] ? 1 : 0;
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
	"math"
	"sync"
	"time"
	"unsafe"
)

const statePollInterval = 250 * time.Millisecond

type Player struct {
	mu      sync.Mutex
	view    unsafe.Pointer
	closed  bool
	cancel  context.CancelFunc
	done    chan struct{}
	onState StateHandler
}

func Start(ctx context.Context, url string, rect Rect, opts Options) (*Player, error) {
	if !rect.Valid() {
		return nil, fmt.Errorf("native player: invalid view rect")
	}
	rawURL := C.CString(url)
	defer C.free(unsafe.Pointer(rawURL))
	htmlControls := 0
	if opts.UseHTMLControls {
		htmlControls = 1
	}
	view := C.tdrive_player_create_view(rawURL, C.double(rect.X), C.double(rect.Y), C.double(rect.Width), C.double(rect.Height), C.int(htmlControls))
	if view == nil {
		return nil, fmt.Errorf("native player: could not start libmpv view")
	}
	runCtx, cancel := context.WithCancel(ctx)
	p := &Player{
		view:    view,
		cancel:  cancel,
		done:    make(chan struct{}),
		onState: opts.OnState,
	}
	if p.onState != nil {
		go p.streamState(runCtx)
	} else {
		close(p.done)
	}
	return p, nil
}

func (p *Player) Resize(rect Rect) error {
	if p == nil {
		return nil
	}
	if !rect.Valid() {
		return fmt.Errorf("native player: invalid view rect")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.view == nil {
		return nil
	}
	C.tdrive_player_set_frame(p.view, C.double(rect.X), C.double(rect.Y), C.double(rect.Width), C.double(rect.Height))
	return nil
}

func (p *Player) Command(command ...string) error {
	if p == nil || len(command) == 0 {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.view == nil {
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
	if rc := C.tdrive_player_command(p.view, C.int(len(command)), (**C.char)(unsafe.Pointer(&cStrings[0]))); rc < 0 {
		return fmt.Errorf("native player: libmpv command failed: %d", int(rc))
	}
	return nil
}

func (p *Player) Close() error {
	if p == nil {
		return nil
	}
	if p.cancel != nil {
		p.cancel()
	}
	if p.done != nil {
		select {
		case <-p.done:
		case <-time.After(time.Second):
		}
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

func (p *Player) streamState(ctx context.Context) {
	defer close(p.done)
	p.emitState()
	ticker := time.NewTicker(statePollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.emitState()
		}
	}
}

func (p *Player) emitState() {
	if p == nil || p.onState == nil {
		return
	}
	state, ok := p.State()
	if !ok {
		return
	}
	p.onState(state)
}

func (p *Player) State() (State, bool) {
	if p == nil {
		return State{}, false
	}
	var raw C.tdrive_player_state
	p.mu.Lock()
	if p.closed || p.view == nil {
		p.mu.Unlock()
		return State{}, false
	}
	ok := C.tdrive_player_snapshot(p.view, &raw) != 0
	p.mu.Unlock()
	if !ok {
		return State{}, false
	}

	currentTime := cleanSeconds(float64(raw.time_pos), raw.has_time_pos != 0)
	duration := cleanSeconds(float64(raw.duration), raw.has_duration != 0)
	volume := clampFloat(float64(raw.volume)/100, 0, 1)
	rate := clampFloat(float64(raw.speed), 0.25, 4)
	if rate == 0 {
		rate = 1
	}
	paused := raw.paused != 0 || raw.eof_reached != 0
	if raw.eof_reached != 0 && duration > 0 {
		currentTime = duration
	}

	state := State{
		Paused:      paused,
		CurrentTime: clampFloat(currentTime, 0, maxPositive(duration, currentTime)),
		Duration:    duration,
		Volume:      volume,
		Muted:       raw.muted != 0 || volume == 0,
		Rate:        rate,
		Loading:     raw.paused_for_cache != 0 || (!paused && raw.cache_buffering_state > 0 && raw.cache_buffering_state < 100),
	}
	if duration > 0 && raw.has_cache_duration != 0 {
		cacheDuration := cleanSeconds(float64(raw.cache_duration), true)
		if cacheDuration > 0 {
			state.Buffered = []BufferedRange{{
				Start: clampFloat(state.CurrentTime, 0, duration),
				End:   clampFloat(state.CurrentTime+cacheDuration, 0, duration),
			}}
		}
	}
	return state, true
}

func cleanSeconds(value float64, ok bool) float64 {
	if !ok || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return 0
	}
	return value
}

func clampFloat(value, min, max float64) float64 {
	if max < min {
		max = min
	}
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

func maxPositive(values ...float64) float64 {
	var max float64
	for _, value := range values {
		if value > max {
			max = value
		}
	}
	return max
}
