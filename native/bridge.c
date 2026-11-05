/*
 * FreeRDPTun 0.1.0. Native FreeRDP transport, no desktop and no fake RDP handshake.
 * SPDX-License-Identifier: Apache-2.0
 * FreeRDP API usage informed by its Apache-2.0 Sample server and echo channel.
 *
 * stdin/stdout: binary, uint32 big-endian length + application message.
 * stdout must NEVER be used for logs. All FreeRDP calls that send network data
 * run on the event-loop thread. The stdin thread only fills a bounded queue.
 */
#include <freerdp/config.h>
#include <freerdp/freerdp.h>
#include <freerdp/peer.h>
#include <freerdp/settings.h>
#include <freerdp/crypto/certificate.h>
#include <freerdp/crypto/privatekey.h>
#include <freerdp/client/cmdline.h>
#include <freerdp/client/channels.h>
#include <freerdp/channels/channels.h>
#include <freerdp/channels/drdynvc.h>
#include <freerdp/channels/wtsvc.h>
#include <freerdp/server/channels.h>
#include <winpr/crt.h>
#include <winpr/synch.h>
#include <winpr/thread.h>
#include <winpr/winsock.h>
#include <winpr/wlog.h>
#include <openssl/pem.h>
#include <openssl/x509.h>
#include <openssl/crypto.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <stdint.h>
#include <signal.h>
#ifdef _WIN32
#include <io.h>
#include <fcntl.h>
#else
#include <unistd.h>
#include <grp.h>
#include <sys/prctl.h>
#endif

#define CHANNEL_NAME "FreeRDPTun::IP::1"
#define MAX_MESSAGE 65535u
#define QUEUE_COUNT 128

typedef struct { BYTE *data; UINT32 length; } item;
static struct {
    CRITICAL_SECTION lock;
    HANDLE stop, available, space, input_thread;
    item queue[QUEUE_COUNT];
    unsigned head, count;
    IWTSVirtualChannel *client_channel;
    BYTE pin[32];
} state;

#ifndef _WIN32
static BOOL drop_privileges(void) {
    if (geteuid() == 0) {
        if (setgroups(0, NULL) || setgid(65534) || setuid(65534)) {
            fprintf(stderr, "rdpt-bridge: cannot drop root privileges\n");
            return FALSE;
        }
    }
    return prctl(PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0) == 0;
}
#endif

static int read_all(BYTE *p, size_t n) {
    while (n) {
        size_t r = fread(p, 1, n, stdin);
        if (!r) return 0;
        p += r; n -= r;
    }
    return 1;
}
static BOOL output(const BYTE *p, size_t n) {
    if (fwrite(p, 1, n, stdout) != n || fflush(stdout)) {
        SetEvent(state.stop);
        return FALSE;
    }
    return TRUE;
}
static DWORD WINAPI input_main(LPVOID unused) {
    (void)unused;
    for (;;) {
        BYTE h[4]; BYTE *p; UINT32 n;
        HANDLE events[2] = {state.stop, state.space};
        if (WaitForMultipleObjects(2, events, FALSE, INFINITE) != WAIT_OBJECT_0 + 1) break;
        if (!read_all(h, 4)) break;
        n = ((UINT32)h[0]<<24)|((UINT32)h[1]<<16)|((UINT32)h[2]<<8)|h[3];
        if (n == 0 || n > MAX_MESSAGE) break;
        p = malloc(n + 4);
        if (!p) break;
        memcpy(p, h, 4);
        if (!read_all(p+4, n)) { free(p); break; }
        EnterCriticalSection(&state.lock);
        unsigned slot = (state.head + state.count) % QUEUE_COUNT;
        state.queue[slot] = (item){p, n+4};
        state.count++;
        if (state.count == QUEUE_COUNT) ResetEvent(state.space);
        SetEvent(state.available);
        LeaveCriticalSection(&state.lock);
    }
    SetEvent(state.stop);
    return 0;
}
static BOOL queue_take(item *i) {
    BOOL found = FALSE;
    EnterCriticalSection(&state.lock);
    if (state.count) {
        *i = state.queue[state.head];
        state.head = (state.head+1) % QUEUE_COUNT;
        state.count--;
        SetEvent(state.space);
        found = TRUE;
    }
    if (!state.count) ResetEvent(state.available);
    LeaveCriticalSection(&state.lock);
    return found;
}

/* Generic DVC plugin; callbacks execute on the client event-loop thread. */
static UINT dvc_data(IWTSVirtualChannelCallback *iface, wStream *data) {
    (void)iface;
    return output(Stream_ConstPointer(data), Stream_GetRemainingLength(data)) ?
        CHANNEL_RC_OK : ERROR_BROKEN_PIPE;
}
static UINT dvc_open(IWTSVirtualChannelCallback *iface) {
    GENERIC_CHANNEL_CALLBACK *c = (GENERIC_CHANNEL_CALLBACK*)iface;
    EnterCriticalSection(&state.lock);
    if (state.client_channel) { LeaveCriticalSection(&state.lock); return ERROR_ALREADY_EXISTS; }
    state.client_channel = c->channel;
    LeaveCriticalSection(&state.lock);
    fprintf(stderr, "rdpt-bridge: DVC connected\n");
    return CHANNEL_RC_OK;
}
static UINT dvc_close(IWTSVirtualChannelCallback *iface) {
    EnterCriticalSection(&state.lock);
    state.client_channel = NULL;
    LeaveCriticalSection(&state.lock);
    SetEvent(state.stop);
    free(iface);
    return CHANNEL_RC_OK;
}
static const IWTSVirtualChannelCallback dvc_callbacks = {
    dvc_data, dvc_open, dvc_close, NULL
};
static UINT VCAPITYPE tunnel_entry(IDRDYNVC_ENTRY_POINTS *points) {
    return freerdp_generic_DVCPluginEntry(points, "rdpt.bridge", CHANNEL_NAME,
        sizeof(GENERIC_DYNVC_PLUGIN), sizeof(GENERIC_CHANNEL_CALLBACK),
        &dvc_callbacks, NULL, NULL);
}
static PVIRTUALCHANNELENTRY provider(LPCSTR name, LPCSTR subsystem, LPCSTR type, DWORD flags) {
    if (name && strcmp(name, "rdptun") == 0 && (flags & FREERDP_ADDIN_CHANNEL_DYNAMIC))
        return (PVIRTUALCHANNELENTRY)tunnel_entry;
    return freerdp_channels_load_static_addin_entry(name, subsystem, type, flags);
}
static BOOL load_channels(freerdp *instance) {
    return freerdp_client_load_addins(instance->context->channels, instance->context->settings);
}
/* Always called through ExternalCertificateManagement, including CA-valid certs.
 * Hash the DER leaf certificate, not its text representation. No accept-any mode. */
static int verify_cert(freerdp *instance, const BYTE *data, size_t length,
                       const char *hostname, UINT16 port, DWORD flags) {
    (void)instance; (void)hostname; (void)port; (void)flags;
    int accepted = -1;
    if (length > INT32_MAX) return -1;
    BIO *bio = BIO_new_mem_buf(data, (int)length);
    X509 *cert = bio ? PEM_read_bio_X509(bio, NULL, NULL, NULL) : NULL;
    BYTE digest[32]; unsigned n = 0;
    if (cert && X509_cmp_current_time(X509_get0_notBefore(cert)) < 0 &&
        X509_cmp_current_time(X509_get0_notAfter(cert)) > 0 &&
        X509_digest(cert, EVP_sha256(), digest, &n) && n == 32 &&
        CRYPTO_memcmp(digest, state.pin, 32) == 0) accepted = 2;
    X509_free(cert); BIO_free(bio);
    if (accepted < 0) fprintf(stderr, "rdpt-bridge: server certificate pin mismatch\n");
    return accepted;
}
static BOOL reject_auth(freerdp *i, char **u, char **p, char **d,
                        rdp_auth_reason reason) {
    (void)i; (void)u; (void)p; (void)d;
    /* TLS without NLA has no OS account login. UUID auth follows inside DVC. */
    return reason == AUTH_TLS;
}
static BOOL client_post_connect(freerdp *instance) {
    (void)instance;
    fprintf(stderr, "rdpt-bridge: RDP session activated\n");
    return TRUE;
}
static int client_run(const char *host, UINT32 port) {
    freerdp *client = freerdp_new();
    int result = 1;
    if (!client || !freerdp_context_new(client)) goto done;
    rdpSettings *s = client->context->settings;
    freerdp_register_addin_provider(provider, 0);
    client->LoadChannels = load_channels;
    client->PostConnect = client_post_connect;
    client->AuthenticateEx = reject_auth;
    client->VerifyX509Certificate = verify_cert;
    const char *args[] = {"rdptun"};
    if (!freerdp_settings_set_string(s, FreeRDP_ServerHostname, host) ||
        !freerdp_settings_set_uint32(s, FreeRDP_ServerPort, port) ||
        !freerdp_settings_set_uint32(s, FreeRDP_DesktopWidth, 800) ||
        !freerdp_settings_set_uint32(s, FreeRDP_DesktopHeight, 600) ||
        !freerdp_settings_set_uint32(s, FreeRDP_ColorDepth, 32) ||
        !freerdp_settings_set_bool(s, FreeRDP_RdpSecurity, FALSE) ||
        !freerdp_settings_set_bool(s, FreeRDP_TlsSecurity, TRUE) ||
        !freerdp_settings_set_bool(s, FreeRDP_NlaSecurity, FALSE) ||
        !freerdp_settings_set_bool(s, FreeRDP_Authentication, FALSE) ||
        !freerdp_settings_set_bool(s, FreeRDP_ExtSecurity, FALSE) ||
        !freerdp_settings_set_uint16(s, FreeRDP_TLSMinVersion, 0x0303) ||
        !freerdp_settings_set_bool(s, FreeRDP_ExternalCertificateManagement, TRUE) ||
        !freerdp_settings_set_bool(s, FreeRDP_DeviceRedirection, FALSE) ||
        !freerdp_settings_set_bool(s, FreeRDP_AudioPlayback, FALSE) ||
        !freerdp_settings_set_bool(s, FreeRDP_AudioCapture, FALSE) ||
        !freerdp_settings_set_bool(s, FreeRDP_RedirectClipboard, FALSE) ||
        !freerdp_settings_set_bool(s, FreeRDP_NetworkAutoDetect, FALSE) ||
        !freerdp_settings_set_bool(s, FreeRDP_SupportHeartbeatPdu, FALSE) ||
        !freerdp_settings_set_bool(s, FreeRDP_SupportMultitransport, FALSE) ||
        !freerdp_settings_set_bool(s, FreeRDP_SupportGraphicsPipeline, FALSE) ||
        !freerdp_settings_set_bool(s, FreeRDP_DeactivateClientDecoding, TRUE) ||
        !freerdp_settings_set_bool(s, FreeRDP_AutoReconnectionEnabled, FALSE) ||
        !freerdp_settings_set_uint32(s, FreeRDP_TcpConnectTimeout, 10000) ||
        !freerdp_client_add_dynamic_channel(s, 1, args)) goto done;
    if (!freerdp_connect(client)) goto done;
    while (WaitForSingleObject(state.stop, 0) != WAIT_OBJECT_0 &&
           !freerdp_shall_disconnect_context(client->context)) {
        HANDLE events[64];
        DWORD n = freerdp_get_event_handles(client->context, events, 60);
        if (!n) break;
        events[n++] = state.stop;
        /* Don't spin on queued data until the channel is actually open. */
        EnterCriticalSection(&state.lock);
        BOOL ready = state.client_channel != NULL;
        LeaveCriticalSection(&state.lock);
        if (ready) events[n++] = state.available;
        DWORD w = WaitForMultipleObjects(n, events, FALSE, 100);
        if (w == WAIT_FAILED || !freerdp_check_event_handles(client->context)) break;
        item i;
        unsigned budget = 32;
        while (ready && budget-- && queue_take(&i)) {
            EnterCriticalSection(&state.lock);
            IWTSVirtualChannel *ch = state.client_channel;
            UINT rc = ch ? ch->Write(ch, i.length, i.data, NULL) : ERROR_BROKEN_PIPE;
            LeaveCriticalSection(&state.lock);
            free(i.data);
            if (rc != CHANNEL_RC_OK) { SetEvent(state.stop); break; }
        }
    }
    result = 0;
    freerdp_disconnect(client);
done:
    if (client) { freerdp_context_free(client); freerdp_free(client); }
    return result;
}

#ifndef _WIN32
typedef struct { rdpContext base; HANDLE vcm; BOOL activated; } server_context;
static BOOL server_context_new(freerdp_peer *peer, rdpContext *base) {
    server_context *ctx = (server_context*)base;
    ctx->vcm = WTSOpenServerA((LPSTR)peer->context);
    return ctx->vcm && ctx->vcm != INVALID_HANDLE_VALUE;
}
static void server_context_free(freerdp_peer *peer, rdpContext *base) {
    (void)peer;
    server_context *ctx = (server_context*)base;
    if (ctx->vcm && ctx->vcm != INVALID_HANDLE_VALUE) WTSCloseServer(ctx->vcm);
}
static BOOL server_activate(freerdp_peer *peer) {
    ((server_context*)peer->context)->activated = TRUE;
    return TRUE;
}
static BOOL server_post_connect(freerdp_peer *peer) { (void)peer; return TRUE; }
static int server_run(int fd, const char *cert_path, const char *key_path) {
    int result = 1;
    HANDLE channel = NULL;
    DWORD session_id = 0;
    BOOL channel_ready = FALSE;
    freerdp_peer *peer = freerdp_peer_new(fd);
    if (!peer) { close(fd); return 1; }
    peer->ContextSize = sizeof(server_context);
    peer->ContextNew = server_context_new;
    peer->ContextFree = server_context_free;
    peer->Activate = server_activate;
    peer->PostConnect = server_post_connect;
    if (!freerdp_peer_context_new(peer)) goto done;
    server_context *ctx = (server_context*)peer->context;
    rdpSettings *s = peer->context->settings;
    LPSTR session_data = NULL;
    DWORD session_size = 0;
    if (!WTSQuerySessionInformationA(ctx->vcm, WTS_CURRENT_SESSION, WTSSessionId,
                                    &session_data, &session_size) ||
        session_size != sizeof(DWORD)) goto done;
    memcpy(&session_id, session_data, sizeof(session_id));
    WTSFreeMemory(session_data);
    rdpPrivateKey *key = freerdp_key_new_from_file_enc(key_path, NULL);
    rdpCertificate *cert = freerdp_certificate_new_from_file(cert_path);
    if (!key || !cert) {
        freerdp_key_free(key); freerdp_certificate_free(cert); goto done;
    }
    if (!freerdp_settings_set_pointer_len(s, FreeRDP_RdpServerRsaKey, key, 1) ||
        !freerdp_settings_set_pointer_len(s, FreeRDP_RdpServerCertificate, cert, 1) ||
        !freerdp_settings_set_bool(s, FreeRDP_RdpSecurity, FALSE) ||
        !freerdp_settings_set_bool(s, FreeRDP_TlsSecurity, TRUE) ||
        !freerdp_settings_set_bool(s, FreeRDP_NlaSecurity, FALSE) ||
        !freerdp_settings_set_bool(s, FreeRDP_Authentication, FALSE) ||
        !freerdp_settings_set_bool(s, FreeRDP_ExtSecurity, FALSE) ||
        !freerdp_settings_set_uint16(s, FreeRDP_TLSMinVersion, 0x0303) ||
        !freerdp_settings_set_bool(s, FreeRDP_NetworkAutoDetect, FALSE) ||
        !freerdp_settings_set_bool(s, FreeRDP_SupportHeartbeatPdu, FALSE) ||
        !freerdp_settings_set_bool(s, FreeRDP_SupportMultitransport, FALSE) ||
        !freerdp_settings_set_uint32(s, FreeRDP_DesktopWidth, 800) ||
        !freerdp_settings_set_uint32(s, FreeRDP_DesktopHeight, 600) ||
        !freerdp_settings_set_uint32(s, FreeRDP_ColorDepth, 32)) goto done;
    /* Load key material first; drop root BEFORE parsing any network input. */
    if (!drop_privileges() || !peer->Initialize(peer)) goto done;
    for (;;) {
        if (WaitForSingleObject(state.stop, 0) == WAIT_OBJECT_0) break;
        HANDLE events[64];
        DWORD n = peer->GetEventHandles(peer, events, 60);
        if (!n) break;
        events[n++] = state.stop;
        events[n++] = WTSVirtualChannelManagerGetEventHandle(ctx->vcm);
        if (channel_ready) events[n++] = state.available;
        DWORD w = WaitForMultipleObjects(n, events, FALSE, 20);
        if (w == WAIT_FAILED || !peer->CheckFileDescriptor(peer)) break;
        /* Do not auto-open DRDYNVC before activation: channel IDs are not
         * assigned during the earlier GCC/MCS stages. */
        if (!ctx->activated) continue;
        if (!WTSVirtualChannelManagerCheckFileDescriptor(ctx->vcm)) break;
        if (!channel && WTSVirtualChannelManagerGetDrdynvcState(ctx->vcm) == DRDYNVC_STATE_READY) {
            channel = WTSVirtualChannelOpenEx(session_id, CHANNEL_NAME, WTS_CHANNEL_OPTION_DYNAMIC);
            if (!channel) break;
        }
        if (channel) {
            if (!channel_ready) {
                PVOID ready = NULL; DWORD size = 0;
                if (!WTSVirtualChannelQuery(channel, WTSVirtualChannelReady, &ready, &size)) break;
                channel_ready = size == sizeof(BOOL) && *(BOOL*)ready;
                WTSFreeMemory(ready);
                if (!channel_ready) continue;
                fprintf(stderr, "rdpt-bridge: DVC connected\n");
            }
            BYTE data[MAX_MESSAGE+4]; ULONG nr = 0;
            while (WTSVirtualChannelRead(channel, 0, (PCHAR)data, sizeof(data), &nr) && nr) {
                if (!output(data, nr)) break;
            }
            item i; unsigned budget = 32;
            while (budget-- && queue_take(&i)) {
                ULONG nw = 0;
                BOOL ok = WTSVirtualChannelWrite(channel, (PCHAR)i.data, i.length, &nw);
                free(i.data);
                if (!ok || nw != i.length) { SetEvent(state.stop); break; }
            }
        }
    }
    result = 0;
done:
    if (channel) WTSVirtualChannelClose(channel);
    if (peer->context && ((server_context*)peer->context)->activated && peer->Close)
        (void)peer->Close(peer);
    if (peer->Disconnect) peer->Disconnect(peer);
    freerdp_peer_context_free(peer);
    freerdp_peer_free(peer);
    return result;
}
#endif

static const char *arg(int argc, char **argv, const char *key) {
    for (int i=1; i+1<argc; i++) if (!strcmp(argv[i], key)) return argv[i+1];
    return NULL;
}
int main(int argc, char **argv) {
    if (argc == 2 && !strcmp(argv[1], "--version")) {
        fprintf(stderr, "FreeRDPTun bridge 0.1.0 / FreeRDP %s\n", freerdp_get_version_string());
        return 0;
    }
#ifdef _WIN32
    _setmode(_fileno(stdin), _O_BINARY); _setmode(_fileno(stdout), _O_BINARY);
#else
    signal(SIGPIPE, SIG_IGN);
    signal(SIGINT, SIG_IGN);
    signal(SIGTERM, SIG_IGN);
#endif
    setvbuf(stdout, NULL, _IONBF, 0);
    setvbuf(stdin, NULL, _IONBF, 0);
    /* Never allow an inherited console appender to corrupt binary stdout. */
    WLog_SetLogAppenderType(WLog_GetRoot(), WLOG_APPENDER_CONSOLE);
    WLog_ConfigureAppender(WLog_GetLogAppender(WLog_GetRoot()), "outputstream", "stderr");
    WLog_SetLogLevel(WLog_GetRoot(), WLOG_WARN);
    WSADATA ws;
    if (WSAStartup(MAKEWORD(2,2), &ws)) return 1;
#ifndef _WIN32
    if (!WTSRegisterWtsApiFunctionTable(FreeRDP_InitWtsApi())) return 1;
#endif
    InitializeCriticalSection(&state.lock);
    state.stop = CreateEvent(NULL, TRUE, FALSE, NULL);
    state.available = CreateEvent(NULL, TRUE, FALSE, NULL);
    state.space = CreateEvent(NULL, TRUE, TRUE, NULL);
    if (!state.stop || !state.available || !state.space) return 1;
    state.input_thread = CreateThread(NULL, 0, input_main, NULL, 0, NULL);
    if (!state.input_thread) return 1;
    int rc = 1;
    const char *host = arg(argc, argv, "--host");
    const char *port = arg(argc, argv, "--port");
    const char *pin = arg(argc, argv, "--pin");
    if (host && port && pin && strlen(pin) == 64) {
        BOOL valid = TRUE;
        for (unsigned i=0; i<32; i++) {
            char h[3] = {pin[i*2], pin[i*2+1], 0}; char *end;
            unsigned long v = strtoul(h, &end, 16);
            if (*end) { valid = FALSE; break; }
            state.pin[i] = (BYTE)v;
        }
        long p = strtol(port, NULL, 10);
        if (valid && p > 0 && p <= 65535) {
#ifndef _WIN32
            if (!drop_privileges()) _Exit(1);
#endif
            rc = client_run(host, (UINT32)p);
        }
    }
#ifndef _WIN32
    else {
        const char *fd = arg(argc, argv, "--server-fd");
        const char *cert = arg(argc, argv, "--cert");
        const char *key = arg(argc, argv, "--key");
        if (fd && cert && key) rc = server_run(atoi(fd), cert, key);
    }
#endif
    /* stdin reader can be blocked in a pipe read; process exit cancels it.
     * It owns no sockets or TUN handles. Parent closes/reaps the process. */
    /* exit() may try to lock/flush stdin while input_thread is blocked in
     * fread(). Both output streams are already unbuffered; exit all threads
     * without stdio shutdown to avoid a failed-connect deadlock. */
    _Exit(rc);
}
