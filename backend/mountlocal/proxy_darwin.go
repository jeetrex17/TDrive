//go:build darwin && !ios && cgo

package mountlocal

/*
#cgo LDFLAGS: -framework CFNetwork -framework CoreFoundation
#include <CFNetwork/CFNetwork.h>
#include <stdlib.h>
#include <netdb.h>
#include <arpa/inet.h>

static int resolves_loopback(const char *hostname) {
 struct addrinfo hints = {0}, *result = NULL;
 hints.ai_family = AF_UNSPEC; hints.ai_socktype = SOCK_STREAM;
 if (getaddrinfo(hostname, NULL, &hints, &result) != 0) return 0;
 int valid = result != NULL;
 for (struct addrinfo *p = result; p; p = p->ai_next) {
  if (p->ai_family != AF_INET || ((struct sockaddr_in *)p->ai_addr)->sin_addr.s_addr != htonl(0x7f000001)) valid = 0;
 }
 freeaddrinfo(result);
 return valid;
}


static int direct_route(const char *url) {
 CFStringRef text = CFStringCreateWithCString(NULL, url, kCFStringEncodingUTF8);
 if (!text) return 0;
 CFURLRef target = CFURLCreateWithString(NULL, text, NULL);
 CFRelease(text);
 if (!target) return 0;
 CFDictionaryRef settings = CFNetworkCopySystemProxySettings();
 if (!settings) { CFRelease(target); return 0; }
 CFArrayRef proxies = CFNetworkCopyProxiesForURL(target, settings);
 CFRelease(settings);
 CFRelease(target);
 if (!proxies) return 0;
 int direct = CFArrayGetCount(proxies) > 0;
 for (CFIndex i = 0; i < CFArrayGetCount(proxies); i++) {
  CFDictionaryRef proxy = (CFDictionaryRef)CFArrayGetValueAtIndex(proxies, i);
  CFTypeRef type = CFDictionaryGetValue(proxy, kCFProxyTypeKey);
  if (!type || !CFEqual(type, kCFProxyTypeNone)) direct = 0;
 }
 CFRelease(proxies);
 return direct;
}
*/
import "C"

import (
	"context"
	"net"
	"unsafe"
)

const nativeAvailable = true

func nativeResolve(ctx context.Context) ([]net.IPAddr, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	name := C.CString(Hostname)
	defer C.free(unsafe.Pointer(name))
	if C.resolves_loopback(name) == 0 {
		return nil, ErrResolution
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return []net.IPAddr{{IP: net.IPv4(127, 0, 0, 1)}}, nil
}

func directProxyRoute(port string) bool {
	// Use the real authority but never give proxy selection a bearer token.
	value := C.CString("http://" + Hostname + ":" + port + "/")
	defer C.free(unsafe.Pointer(value))
	return C.direct_route(value) != 0
}
