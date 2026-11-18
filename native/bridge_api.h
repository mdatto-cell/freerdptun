/* In-process API of the native bridge (compiled with -DFRT_LIBRARY, iOS). */
#ifndef FRT_BRIDGE_API_H
#define FRT_BRIDGE_API_H
#ifdef __cplusplus
extern "C" {
#endif
/* Blocks until the RDP session ends. pin is 64 hex chars (SHA-256 of DER leaf). */
int rdpt_bridge_run(const char *host, unsigned port, const char *pin, int in_fd, int out_fd);
/* Thread-safe; may be called repeatedly. */
void rdpt_bridge_stop(void);
#ifdef __cplusplus
}
#endif
#endif
