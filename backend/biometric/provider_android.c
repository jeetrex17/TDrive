//go:build android && cgo

#include "../thumbnail/android_jni.h"
#include <stdint.h>

static tdrive_jni_binding vault_binding = TDRIVE_JNI_BINDING_INIT;

JNIEXPORT void JNICALL Java_com_wails_app_BiometricVault_nativeInit(JNIEnv *env, jclass cls) {
    tdrive_jni_bind(&vault_binding, env, cls, "invoke", "(JILjava/lang/String;Ljava/lang/String;[B)[B");
}

static void vault_wipe_java(JNIEnv *env, jbyteArray array) {
    if (!array) return;
    if ((*env)->ExceptionCheck(env)) (*env)->ExceptionClear(env);
    jsize length = (*env)->GetArrayLength(env, array);
    if (length > 0 && length <= 65) {
        jbyte zeros[65] = {0};
        (*env)->SetByteArrayRegion(env, array, 0, length, zeros);
    }
    if ((*env)->ExceptionCheck(env)) (*env)->ExceptionClear(env);
}

// The returned buffer is owned by Go, which explicitly wipes it before free.
int tdrive_android_biometric_call(uint64_t id, int operation, const char *scope,
                                 const char *config, const void *secret, size_t secret_length,
                                 void **bytes, size_t *length) {
    *bytes = NULL;
    *length = 0;
    JavaVM *vm;
    jclass cls;
    jmethodID method;
    if (!tdrive_jni_resolve(&vault_binding, &vm, &cls, &method)) return 1;
    int attached = 0;
    JNIEnv *env = tdrive_jni_attach(vm, &attached);
    if (!env) return 1;
    if ((*env)->PushLocalFrame(env, 8) < 0) {
        if ((*env)->ExceptionCheck(env)) (*env)->ExceptionClear(env);
        tdrive_jni_detach(vm, attached);
        return 1;
    }
    jstring scope_string = (*env)->NewStringUTF(env, scope);
    jstring config_string = (*env)->NewStringUTF(env, config);
    jbyteArray input = NULL;
    if (secret && secret_length == 64) {
        input = (*env)->NewByteArray(env, 64);
        if (input) (*env)->SetByteArrayRegion(env, input, 0, 64, (const jbyte *)secret);
    }
    if (scope_string && config_string && !(*env)->ExceptionCheck(env)) {
        jbyteArray result = (jbyteArray)(*env)->CallStaticObjectMethod(env, cls, method,
                (jlong)id, (jint)operation, scope_string, config_string, input);
        if (result && !(*env)->ExceptionCheck(env)) {
            jsize count = (*env)->GetArrayLength(env, result);
            if (count > 0 && count <= 65) tdrive_jni_take_bytes(env, result, bytes, length);
        }
        vault_wipe_java(env, result);
    }
    vault_wipe_java(env, input);
    if ((*env)->ExceptionCheck(env)) (*env)->ExceptionClear(env);
    (*env)->PopLocalFrame(env, NULL);
    tdrive_jni_detach(vm, attached);
    return *bytes ? 0 : 1;
}

void tdrive_android_biometric_cancel(uint64_t id) {
    JavaVM *vm;
    jclass cls;
    jmethodID ignored;
    if (!tdrive_jni_resolve(&vault_binding, &vm, &cls, &ignored)) return;
    int attached = 0;
    JNIEnv *env = tdrive_jni_attach(vm, &attached);
    if (!env) return;
    jmethodID cancel = (*env)->GetStaticMethodID(env, cls, "cancel", "(J)V");
    if (cancel && !(*env)->ExceptionCheck(env)) {
        (*env)->CallStaticVoidMethod(env, cls, cancel, (jlong)id);
    }
    if ((*env)->ExceptionCheck(env)) (*env)->ExceptionClear(env);
    tdrive_jni_detach(vm, attached);
}
