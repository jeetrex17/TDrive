// Shared JNI plumbing for the bridges from Go into the app's Java helpers.
//
// Every bridge here has the same shape: the Java host binds a static method
// once at startup, then Go worker threads call it and copy a byte[] back. That
// mechanism lives here exactly once; a bridge file contributes only its own
// cached binding and the arguments its method takes.
//
// Header-only because there is nothing to share at link time -- each binding is
// private storage in its own translation unit, and the functions are small
// enough that inlining them is cheaper than a call.

#ifndef TDRIVE_ANDROID_JNI_H
#define TDRIVE_ANDROID_JNI_H

#include <jni.h>
#include <pthread.h>
#include <stdlib.h>

// The largest byte[] any bridge will copy out of Java. Both a sampled photo and
// a video poster are tens of kilobytes; a result past this means the Java side
// returned something it should not have, and the allocation is refused rather
// than trusted.
#define TDRIVE_JNI_MAX_RESULT (8 * 1024 * 1024)

// tdrive_jni_binding caches one static Java method across calls. Zero value is
// unbound, which is a legitimate state: a CLI or test host has no activity to
// bind it, and the caller falls back rather than failing.
typedef struct {
    pthread_mutex_t lock;
    JavaVM *vm;
    jclass cls; // global reference, held for the process lifetime
    jmethodID method;
} tdrive_jni_binding;

#define TDRIVE_JNI_BINDING_INIT {PTHREAD_MUTEX_INITIALIZER, NULL, NULL, NULL}

// tdrive_jni_bind caches cls and one of its static methods.
//
// It must be called from Java, on a thread whose class loader is the app's:
// FindClass on an attached Go worker uses the bootstrap loader, which cannot
// see our classes at all. Binding twice is a no-op, so a host that restarts an
// activity does not leak a second global reference.
static inline void tdrive_jni_bind(tdrive_jni_binding *binding, JNIEnv *env, jclass cls,
                                   const char *name, const char *signature) {
    pthread_mutex_lock(&binding->lock);
    if (!binding->cls) {
        jmethodID method = (*env)->GetStaticMethodID(env, cls, name, signature);
        if (method && !(*env)->ExceptionCheck(env)) {
            jclass reference = (*env)->NewGlobalRef(env, cls);
            if (reference && (*env)->GetJavaVM(env, &binding->vm) == JNI_OK) {
                binding->cls = reference;
                binding->method = method;
            } else if (reference) {
                (*env)->DeleteGlobalRef(env, reference);
            }
        }
        if ((*env)->ExceptionCheck(env)) (*env)->ExceptionClear(env);
    }
    pthread_mutex_unlock(&binding->lock);
}

// tdrive_jni_resolve reads a binding under its lock, reporting whether the Java
// host ever bound it.
static inline int tdrive_jni_resolve(tdrive_jni_binding *binding, JavaVM **vm, jclass *cls, jmethodID *method) {
    pthread_mutex_lock(&binding->lock);
    *vm = binding->vm;
    *cls = binding->cls;
    *method = binding->method;
    pthread_mutex_unlock(&binding->lock);
    return *vm && *cls && *method;
}

// tdrive_jni_attach gives the calling Go worker a JNIEnv, recording in
// *attached whether it now owns a detach.
static inline JNIEnv *tdrive_jni_attach(JavaVM *vm, int *attached) {
    JNIEnv *env = NULL;
    *attached = 0;
    if ((*vm)->GetEnv(vm, (void **)&env, JNI_VERSION_1_6) == JNI_OK) return env;
    if ((*vm)->AttachCurrentThread(vm, &env, NULL) != JNI_OK) return NULL;
    *attached = 1;
    return env;
}

// tdrive_jni_detach releases a thread this bridge attached, and only such a
// thread: detaching one the VM owns would tear down its JNIEnv underneath it.
static inline void tdrive_jni_detach(JavaVM *vm, int attached) {
    if (attached) (*vm)->DetachCurrentThread(vm);
}

// tdrive_jni_take_bytes copies a Java byte[] into a malloc'd buffer the Go side
// owns and frees. Returns 0 on success.
//
// A copy rather than a pinned critical section: the Go caller keeps the bytes
// past this call, and holding a JNI critical region across that would block the
// collector of a runtime we do not control.
static inline int tdrive_jni_take_bytes(JNIEnv *env, jbyteArray result, void **bytes, size_t *length) {
    if (!result || (*env)->ExceptionCheck(env)) return 1;
    jsize count = (*env)->GetArrayLength(env, result);
    if (count <= 0 || count > TDRIVE_JNI_MAX_RESULT) return 1;
    void *copy = malloc((size_t)count);
    if (!copy) return 1;
    (*env)->GetByteArrayRegion(env, result, 0, count, copy);
    if ((*env)->ExceptionCheck(env)) {
        free(copy);
        return 1;
    }
    *bytes = copy;
    *length = (size_t)count;
    return 0;
}

// tdrive_jni_utf8 wraps bytes as a Java byte[] rather than a jstring.
//
// JNI strings are modified UTF-8, which cannot carry the characters outside the
// basic multilingual plane that camera filenames routinely contain; the Java
// side decodes these bytes as real UTF-8 instead.
static inline jbyteArray tdrive_jni_utf8(JNIEnv *env, const char *text, size_t length) {
    jbyteArray array = (*env)->NewByteArray(env, (jsize)length);
    if (!array) return NULL;
    (*env)->SetByteArrayRegion(env, array, 0, (jsize)length, (const jbyte *)text);
    if ((*env)->ExceptionCheck(env)) return NULL;
    return array;
}

#endif // TDRIVE_ANDROID_JNI_H
