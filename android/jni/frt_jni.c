/* JNI glue: Kotlin <-> Go core (libfrtcore.so) + in-process FreeRDP bridge. */
#include <jni.h>
#include <fcntl.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <android/log.h>
#include "bridge_api.h"

/* Exported by libfrtcore.so (mobile/core/main.go). */
extern void FRTRegisterBridge(void *run, void *stop);
extern int FRTStart(char *cfg, char *errbuf, int errlen);
extern void FRTStop(void);
extern int FRTStatus(void);
extern void FRTLastError(char *buf, int n);
extern void FRTPushPacket(uint8_t *p, int n);
extern int FRTPopPacket(uint8_t *p, int capacity, int timeoutMs);

#define JNI(name) Java_com_example_freerdptun_Native_##name

JNIEXPORT void JNICALL JNI(init)(JNIEnv *env, jobject thiz, jstring logPath) {
    (void)thiz;
    const char *path = (*env)->GetStringUTFChars(env, logPath, NULL);
    if (path) {
        /* stderr (Go log + FreeRDP WLog + bridge) -> app log file */
        int fd = open(path, O_WRONLY | O_CREAT | O_TRUNC, 0600);
        if (fd >= 0) { dup2(fd, 2); close(fd); }
        (*env)->ReleaseStringUTFChars(env, logPath, path);
    }
    FRTRegisterBridge((void *)rdpt_bridge_run, (void *)rdpt_bridge_stop);
}

/* Returns null on success, otherwise an error message. */
JNIEXPORT jstring JNICALL JNI(start)(JNIEnv *env, jobject thiz, jstring cfg) {
    (void)thiz;
    const char *c = (*env)->GetStringUTFChars(env, cfg, NULL);
    char err[512] = {0};
    int rc = c ? FRTStart((char *)c, err, sizeof err) : 1;
    if (c) (*env)->ReleaseStringUTFChars(env, cfg, c);
    return rc == 0 ? NULL : (*env)->NewStringUTF(env, err[0] ? err : "start failed");
}

JNIEXPORT void JNICALL JNI(stop)(JNIEnv *env, jobject thiz) { (void)env; (void)thiz; FRTStop(); }

JNIEXPORT jint JNICALL JNI(status)(JNIEnv *env, jobject thiz) { (void)env; (void)thiz; return FRTStatus(); }

JNIEXPORT jstring JNICALL JNI(lastError)(JNIEnv *env, jobject thiz) {
    (void)thiz;
    char buf[512] = {0};
    FRTLastError(buf, sizeof buf);
    return (*env)->NewStringUTF(env, buf);
}

JNIEXPORT void JNICALL JNI(push)(JNIEnv *env, jobject thiz, jbyteArray arr, jint len) {
    (void)thiz;
    if (len <= 0 || len > 65535) return;
    jbyte *b = (*env)->GetPrimitiveArrayCritical(env, arr, NULL);
    if (!b) return;
    FRTPushPacket((uint8_t *)b, len);
    (*env)->ReleasePrimitiveArrayCritical(env, arr, b, JNI_ABORT);
}

/* Returns length, 0 on timeout, -1 when the tunnel ended. */
JNIEXPORT jint JNICALL JNI(pop)(JNIEnv *env, jobject thiz, jbyteArray arr, jint timeoutMs) {
    (void)thiz;
    uint8_t tmp[65536];
    int cap = (*env)->GetArrayLength(env, arr);
    if (cap > (int)sizeof tmp) cap = sizeof tmp;
    int n = FRTPopPacket(tmp, cap, timeoutMs);
    if (n > 0) (*env)->SetByteArrayRegion(env, arr, 0, n, (const jbyte *)tmp);
    return n;
}
