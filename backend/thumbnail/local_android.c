//go:build android && cgo

#include <jni.h>
#include <pthread.h>
#include <stdlib.h>
#include <string.h>

static pthread_mutex_t image_lock = PTHREAD_MUTEX_INITIALIZER;
static JavaVM *image_vm;
static jclass image_class;
static jmethodID image_method;

// Called once by the Java host after loading libwails. FindClass on an attached
// Go worker would otherwise use the bootstrap loader, which cannot find our app.
JNIEXPORT void JNICALL Java_com_wails_app_GalleryImage_nativeInit(JNIEnv *env, jclass cls) {
    pthread_mutex_lock(&image_lock);
    if (!image_class) {
        jmethodID method = (*env)->GetStaticMethodID(env, cls, "downsample", "([BIZI)[B");
        if (method && !(*env)->ExceptionCheck(env)) {
            jclass reference = (*env)->NewGlobalRef(env, cls);
            if (reference && (*env)->GetJavaVM(env, &image_vm) == JNI_OK) {
                image_class = reference;
                image_method = method;
            } else if (reference) { (*env)->DeleteGlobalRef(env, reference); }
        }
        if ((*env)->ExceptionCheck(env)) (*env)->ExceptionClear(env);
    }
    pthread_mutex_unlock(&image_lock);
}

int tdrive_android_thumbnail(const char *path, size_t pathLength, int edge, int encoded, int orientation, void **bytes, size_t *length) {
    *bytes = NULL; *length = 0;
    pthread_mutex_lock(&image_lock);
    JavaVM *vm = image_vm;
    jclass cls = image_class;
    jmethodID method = image_method;
    pthread_mutex_unlock(&image_lock);
    if (!vm || !cls || !method) return 3;
    JNIEnv *env = NULL;
    int attached = 0;
    if ((*vm)->GetEnv(vm, (void **)&env, JNI_VERSION_1_6) != JNI_OK) {
        if ((*vm)->AttachCurrentThread(vm, &env, NULL) != JNI_OK) return 1;
        attached = 1;
    }
    if ((*env)->PushLocalFrame(env, 4) < 0) {
        if ((*env)->ExceptionCheck(env)) (*env)->ExceptionClear(env);
        if (attached) (*vm)->DetachCurrentThread(vm);
        return 1;
    }
    // Pass UTF-8 bytes rather than JNI modified UTF-8: camera filenames may
    // contain emoji or other characters outside the basic multilingual plane.
    jbyteArray name = (*env)->NewByteArray(env, (jsize)pathLength);
    if (name) (*env)->SetByteArrayRegion(env, name, 0, (jsize)pathLength, (const jbyte *)path);
    jbyteArray result = NULL;
    if (name && !(*env)->ExceptionCheck(env)) {
        result = (jbyteArray)(*env)->CallStaticObjectMethod(env, cls, method, name, (jint)edge, (jboolean)encoded, (jint)orientation);
    }
    if (result && !(*env)->ExceptionCheck(env)) {
        jsize count = (*env)->GetArrayLength(env, result);
        if (count > 0 && count <= 8 * 1024 * 1024) {
            void *copy = malloc(count);
            if (copy) {
                (*env)->GetByteArrayRegion(env, result, 0, count, copy);
                if (!(*env)->ExceptionCheck(env)) { *bytes = copy; *length = count; }
                else free(copy);
            }
        }
    }
    if ((*env)->ExceptionCheck(env)) (*env)->ExceptionClear(env);
    (*env)->PopLocalFrame(env, NULL);
    if (attached) (*vm)->DetachCurrentThread(vm);
    return *bytes ? 0 : 1;
}
