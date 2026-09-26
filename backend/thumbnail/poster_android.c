//go:build android && cgo

#include "android_jni.h"

static tdrive_jni_binding poster_binding = TDRIVE_JNI_BINDING_INIT;

// Called once by the Java host after loading libwails.
JNIEXPORT void JNICALL Java_com_wails_app_GalleryVideo_nativeInit(JNIEnv *env, jclass cls) {
    tdrive_jni_bind(&poster_binding, env, cls, "poster", "([BIIJ)[B");
}

// tdrive_android_poster asks the Java side for one frame, encoded as a JPEG.
//
// The frame is chosen by seekPercent of the container's declared duration, or
// by fallbackMicros when it declares none. Both come from Go so that the single
// statement of that policy lives with the rest of the poster contract rather
// than being written once per platform.
//
// Returns 0 with a malloc'd buffer the caller frees, 3 when no Java host has
// bound the bridge, and 1 for every "cannot draw this" -- all of which the Go
// side reports as ErrPosterUnsupported.
int tdrive_android_poster(const char *path, size_t pathLength, int edge, int seekPercent, long long fallbackMicros, void **bytes, size_t *length) {
    *bytes = NULL;
    *length = 0;
    JavaVM *vm;
    jclass cls;
    jmethodID method;
    if (!tdrive_jni_resolve(&poster_binding, &vm, &cls, &method)) return 3;
    int attached = 0;
    JNIEnv *env = tdrive_jni_attach(vm, &attached);
    if (!env) return 1;
    if ((*env)->PushLocalFrame(env, 4) < 0) {
        if ((*env)->ExceptionCheck(env)) (*env)->ExceptionClear(env);
        tdrive_jni_detach(vm, attached);
        return 1;
    }
    jbyteArray name = tdrive_jni_utf8(env, path, pathLength);
    if (name) {
        jbyteArray result = (jbyteArray)(*env)->CallStaticObjectMethod(
            env, cls, method, name, (jint)edge, (jint)seekPercent, (jlong)fallbackMicros);
        tdrive_jni_take_bytes(env, result, bytes, length);
    }
    // MediaMetadataRetriever throws on an unreadable file. That is an ordinary
    // answer here, so the exception is cleared and reported as "no frame"; the
    // Java side already catches the ones it can name.
    if ((*env)->ExceptionCheck(env)) (*env)->ExceptionClear(env);
    (*env)->PopLocalFrame(env, NULL);
    tdrive_jni_detach(vm, attached);
    return *bytes ? 0 : 1;
}
