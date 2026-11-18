#ifndef FRT_CORE_H
#define FRT_CORE_H
#include <stdint.h>
#include "bridge_api.h"

/* Exported by libfrtcore.a (Go, ios/core/main.go). */
void FRTRegisterBridge(void *run, void *stop);
int FRTStart(const char *cfg, char *errbuf, int errlen);
void FRTStop(void);
int FRTStatus(void); /* 0 idle, 1 connecting, 2 connected, 3 stopped */
void FRTLastError(char *buf, int n);
void FRTPushPacket(const uint8_t *p, int n);
int FRTPopPacket(uint8_t *p, int capacity, int timeoutMs);

/* frt_glue.c */
void frt_register_bridge(void);
#endif
