#include "FRTCore.h"

/* The Go archive cannot reference the C bridge directly (cgo needs every
 * symbol at its own link step), so the pointers are handed over at runtime. */
void frt_register_bridge(void) {
    FRTRegisterBridge((void *)rdpt_bridge_run, (void *)rdpt_bridge_stop);
}
