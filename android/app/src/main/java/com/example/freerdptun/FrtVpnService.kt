package com.example.freerdptun

import android.content.Context
import android.content.Intent
import android.net.VpnService
import android.os.ParcelFileDescriptor
import android.system.ErrnoException
import android.system.Os
import android.system.OsConstants
import android.system.StructPollfd
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow

enum class VpnStatus { IDLE, CONNECTING, CONNECTED, DISCONNECTING }

data class VpnState(
    val status: VpnStatus = VpnStatus.IDLE,
    val activeId: String? = null,
    val error: String? = null,
) {
    val isActive get() = status != VpnStatus.IDLE
}

object VpnStateHolder {
    private val _state = MutableStateFlow(VpnState())
    val state: StateFlow<VpnState> = _state.asStateFlow()
    fun set(f: (VpnState) -> VpnState) { _state.value = f(_state.value) }
    fun clearError() = set { it.copy(error = null) }
}

/**
 * VpnService: весь IPv4-трафик устройства (0.0.0.0/0) направляется в TUN, откуда пакеты
 * уходят в Go-ядро и дальше через FreeRDP DVC на сервер. Сам процесс приложения исключён из VPN
 * (addDisallowedApplication), поэтому сокет FreeRDP идёт напрямую и петли не возникает.
 */
class FrtVpnService : VpnService() {
    @Volatile private var session: Session? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        when (intent?.action) {
            ACTION_CONNECT -> {
                Native.ensureInit(this)
                session?.requestStop()
                val s = Session(
                    id = intent.getStringExtra("id") ?: "",
                    config = intent.getStringExtra("config") ?: "",
                    dns = intent.getStringExtra("dns") ?: "",
                    blockIPv6 = intent.getBooleanExtra("blockIPv6", true),
                )
                session = s
                VpnStateHolder.set { VpnState(VpnStatus.CONNECTING, s.id, null) }
                Thread(s, "frt.session").start()
            }
            ACTION_DISCONNECT -> {
                session?.requestStop()
                if (session == null) stopSelf()
            }
        }
        return START_NOT_STICKY
    }

    override fun onRevoke() {
        session?.requestStop()
        super.onRevoke()
    }

    override fun onDestroy() {
        session?.requestStop()
        super.onDestroy()
    }

    private inner class Session(
        val id: String, val config: String, val dns: String, val blockIPv6: Boolean,
    ) : Runnable {
        @Volatile private var stopRequested = false
        @Volatile private var pfd: ParcelFileDescriptor? = null

        fun requestStop() {
            if (stopRequested) return
            stopRequested = true
            VpnStateHolder.set { if (it.activeId == id) it.copy(status = VpnStatus.DISCONNECTING) else it }
            Thread { Native.stop() }.start()
        }

        private fun fail(msg: String) {
            VpnStateHolder.set { it.copy(error = msg) }
        }

        override fun run() {
            var ok = false
            try {
                val cfg = TunnelConfig.parse(config)
                val err = Native.start(config)
                if (err != null) { fail(err); return }
                val deadline = System.currentTimeMillis() + (cfg.handshakeTimeoutSeconds + 15) * 1000L
                while (true) {
                    if (stopRequested) return
                    val st = Native.status()
                    if (st == 2) break
                    if (st == 3) {
                        fail(Native.lastError().ifEmpty { "Соединение завершено" })
                        return
                    }
                    if (System.currentTimeMillis() > deadline) { fail("Таймаут подключения"); return }
                    Thread.sleep(100)
                }
                val tun = establish(cfg)
                if (tun == null) { fail("Не удалось создать VPN-интерфейс (нет разрешения?)"); return }
                pfd = tun
                ok = true
                VpnStateHolder.set { it.copy(status = VpnStatus.CONNECTED) }
                pump(tun)
                if (!stopRequested) {
                    fail(Native.lastError().ifEmpty { "Соединение закрыто сервером" })
                }
            } catch (e: Exception) {
                fail(e.message ?: e.toString())
            } finally {
                Native.stop()
                runCatching { pfd?.close() }
                pfd = null
                if (session === this) {
                    session = null
                    VpnStateHolder.set { VpnState(VpnStatus.IDLE, null, it.error.takeIf { _ -> !stopRequested || !ok }) }
                    stopSelf()
                }
            }
        }

        private fun establish(cfg: TunnelConfig): ParcelFileDescriptor? {
            val (addr, bits) = cfg.cidrParts ?: return null
            val b = Builder()
                .setSession("FreeRDPTun")
                .setMtu(cfg.mtu)
                .setBlocking(false)
                .addAddress(addr, bits)
                .addRoute("0.0.0.0", 0)   // весь IPv4-трафик в туннель
            for (d in dns.split(',', ' ', ';', '\n').map { it.trim() }.filter { TunnelConfig.parseIPv4(it) != null }) {
                b.addDnsServer(d)
            }
            if (blockIPv6) {
                // Туннель только IPv4: перехватываем IPv6, чтобы он не утекал (пакеты отбрасываются).
                b.addAddress("fd77:77::2", 64).addRoute("::", 0)
            }
            runCatching { b.addDisallowedApplication(packageName) }
            return b.establish()
        }

        private fun pump(tun: ParcelFileDescriptor) {
            val fd = tun.fileDescriptor
            val writer = Thread({
                val buf = ByteArray(65536)
                while (!stopRequested) {
                    val n = Native.pop(buf, 500)
                    if (n < 0) break
                    if (n > 0) {
                        try { Os.write(fd, buf, 0, n) } catch (_: ErrnoException) { }
                    }
                }
                stopRequested = true
            }, "frt.tun-write")
            writer.start()

            val buf = ByteArray(65536)
            val pfds = arrayOf(StructPollfd().apply { this.fd = fd; events = OsConstants.POLLIN.toShort() })
            while (!stopRequested) {
                try {
                    if (Os.poll(pfds, 250) <= 0) continue
                    val n = Os.read(fd, buf, 0, buf.size)
                    if (n <= 0) continue
                    if ((buf[0].toInt() shr 4) == 4) Native.push(buf, n)  // только IPv4
                } catch (e: ErrnoException) {
                    if (e.errno == OsConstants.EAGAIN || e.errno == OsConstants.EINTR) continue
                    break
                }
            }
            writer.join(2000)
        }
    }

    companion object {
        const val ACTION_CONNECT = "com.example.freerdptun.CONNECT"
        const val ACTION_DISCONNECT = "com.example.freerdptun.DISCONNECT"

        fun connect(ctx: Context, p: Profile) {
            val i = Intent(ctx, FrtVpnService::class.java).setAction(ACTION_CONNECT)
                .putExtra("id", p.id).putExtra("config", p.config.jsonString())
                .putExtra("dns", p.dnsServers).putExtra("blockIPv6", p.blockIPv6)
            ctx.startService(i)
        }

        fun disconnect(ctx: Context) {
            ctx.startService(Intent(ctx, FrtVpnService::class.java).setAction(ACTION_DISCONNECT))
        }
    }
}
