package com.example.freerdptun

import org.json.JSONException
import org.json.JSONObject
import org.json.JSONTokener

/** Конфигурация клиента, 1:1 с Go `tunnel.Config` (режим client). */
data class TunnelConfig(
    val mode: String = "client",
    val address: String = "",
    val tun: String = "frdptun0",
    val cidr: String = "",
    val mtu: Int = 1280,
    val bridge: String = "",
    val certificate: String = "",
    val privateKey: String = "",
    val serverSha256: String = "",
    val uuid: String = "",
    val ipv4: String = "",
    val totalUploadBps: Long = 0,
    val totalDownloadBps: Long = 0,
    val maxConnections: Int = 32,
    val handshakeTimeoutSeconds: Int = 20,
    val idleTimeoutSeconds: Int = 60,
    val queuePackets: Int = 256,
) {
    class ParseError(message: String) : Exception(message)

    val host: String
        get() {
            val i = address.lastIndexOf(':')
            return if (i < 0) address else address.substring(0, i).trim('[', ']')
        }
    val port: Int?
        get() {
            val i = address.lastIndexOf(':')
            return if (i < 0) null else address.substring(i + 1).toIntOrNull()
        }

    /** (адрес, длина префикса) из cidr. */
    val cidrParts: Pair<String, Int>?
        get() {
            val p = cidr.split("/")
            if (p.size != 2) return null
            val bits = p[1].toIntOrNull() ?: return null
            if (bits !in 0..32 || parseIPv4(p[0]) == null) return null
            return p[0] to bits
        }

    fun jsonString(): String {
        fun q(s: String) = JSONObject.quote(s).replace("\\/", "/")
        val lines = listOf(
            "\"mode\": ${q(mode)}", "\"address\": ${q(address)}", "\"tun\": ${q(tun)}",
            "\"cidr\": ${q(cidr)}", "\"mtu\": $mtu", "\"bridge\": ${q(bridge)}",
            "\"certificate\": ${q(certificate)}", "\"private_key\": ${q(privateKey)}",
            "\"server_sha256\": ${q(serverSha256)}", "\"uuid\": ${q(uuid)}",
            "\"ipv4\": ${q(ipv4)}", "\"total_upload_bps\": $totalUploadBps",
            "\"total_download_bps\": $totalDownloadBps", "\"max_connections\": $maxConnections",
            "\"handshake_timeout_seconds\": $handshakeTimeoutSeconds",
            "\"idle_timeout_seconds\": $idleTimeoutSeconds", "\"queue_packets\": $queuePackets",
            "\"clients\": null",
        )
        return "{\n" + lines.joinToString(",\n") { "  $it" } + "\n}"
    }

    /** Проверка, зеркалящая tunnel.Config.Validate для mode=client. Пустой список = всё хорошо. */
    fun validate(): List<String> {
        val e = mutableListOf<String>()
        if (mode != "client") e += "mode: должен быть client"
        val pt = port
        if (host.isEmpty() || pt == null || pt !in 1..65535) e += "address: нужен формат host:порт (порт 1..65535)"
        if (tun.isEmpty() || tun.length > 15 || tun.any { it in " /\\\u0000" }) {
            e += "tun: имя 1..15 символов без пробелов и / \\"
        }
        var cidrAddr: Long? = null
        val c = cidrParts
        if (c != null) {
            val a = parseIPv4(c.first)!!
            cidrAddr = a
            val mask = if (c.second == 0) 0L else (0xFFFFFFFFL shl (32 - c.second)) and 0xFFFFFFFFL
            val host = a and mask.inv() and 0xFFFFFFFFL
            if (c.second <= 30 && (host == 0L || host == (mask.inv() and 0xFFFFFFFFL))) {
                e += "cidr: нужен адрес хоста, а не сети/broadcast"
            }
        } else {
            e += "cidr: нужен IPv4 CIDR, например 10.77.0.2/24"
        }
        if (mtu !in 576..9000) e += "mtu: 576..9000"
        if (handshakeTimeoutSeconds !in 3..120) e += "handshake_timeout_seconds: 3..120"
        if (idleTimeoutSeconds !in 15..3600) e += "idle_timeout_seconds: 15..3600"
        if (maxConnections !in 1..256) e += "max_connections: 1..256"
        if (queuePackets !in 16..4096) e += "queue_packets: 16..4096"
        val maxRate = 1_000_000_000_000L
        if (totalUploadBps !in 0..maxRate || totalDownloadBps !in 0..maxRate) {
            e += "total_upload_bps / total_download_bps: 0..1000000000000"
        }
        val hex = uuid.replace("-", "")
        val dashes = uuid.length == 36 && listOf(8, 13, 18, 23).all { uuid[it] == '-' }
        if (uuid.length != 36 || !dashes || hex.length != 32 || !hex.all { it.isHex() } || hex.all { it == '0' }) {
            e += "uuid: формат 8-4-4-4-12, не нулевой"
        }
        val ip = parseIPv4(ipv4)
        if (ip != null) {
            if (cidrAddr != null && cidrAddr != ip) e += "ipv4: должен совпадать с адресом в cidr"
        } else {
            e += "ipv4: нужен IPv4-адрес"
        }
        if (serverSha256.length != 64 || !serverSha256.all { it.isHex() }) {
            e += "server_sha256: 64 hex-символа (SHA-256 DER листового сертификата)"
        }
        return e
    }

    companion object {
        private val KNOWN = setOf(
            "mode", "address", "tun", "cidr", "mtu", "bridge", "certificate", "private_key",
            "server_sha256", "uuid", "ipv4", "total_upload_bps", "total_download_bps",
            "max_connections", "handshake_timeout_seconds", "idle_timeout_seconds",
            "queue_packets", "clients",
        )

        private fun Char.isHex() = this in '0'..'9' || this in 'a'..'f' || this in 'A'..'F'

        fun parseIPv4(s: String): Long? {
            val parts = s.split(".")
            if (parts.size != 4) return null
            var v = 0L
            for (p in parts) {
                if (p.isEmpty() || p.length > 3 || !p.all { it in '0'..'9' }) return null
                val n = p.toInt()
                if (n > 255) return null
                v = v shl 8 or n.toLong()
            }
            return v
        }

        /** Разбор вставленного текста. Неизвестные ключи отклоняются, как в Go-клиенте. */
        fun parse(text: String): TunnelConfig {
            val t = text.trim()
            if (t.isEmpty()) throw ParseError("Пустая конфигурация")
            val obj = try {
                JSONTokener(t).nextValue() as? JSONObject ?: throw ParseError("Ожидался JSON-объект")
            } catch (ex: JSONException) {
                throw ParseError("Некорректный JSON: ${ex.message}")
            }
            val unknown = obj.keys().asSequence().filter { it !in KNOWN }.sorted().toList()
            if (unknown.isNotEmpty()) {
                throw ParseError("Неизвестные поля (в клиенте не поддерживаются): " + unknown.joinToString(", "))
            }
            if (obj.has("clients") && !obj.isNull("clients")) {
                val arr = obj.optJSONArray("clients")
                if (arr == null || arr.length() != 0) throw ParseError("Поле clients относится только к серверу")
            }
            fun str(k: String, d: String): String {
                if (!obj.has(k) || obj.isNull(k)) return d
                val v = obj.get(k)
                if (v !is String) throw ParseError("Поле $k: ожидалась строка")
                return v
            }
            fun lng(k: String, d: Long): Long {
                if (!obj.has(k) || obj.isNull(k)) return d
                val v = obj.get(k)
                if (v is Int) return v.toLong()
                if (v is Long) return v
                throw ParseError("Поле $k: ожидалось целое число")
            }
            fun int(k: String, d: Int): Int {
                val v = lng(k, d.toLong())
                if (v !in Int.MIN_VALUE..Int.MAX_VALUE) throw ParseError("Поле $k: слишком большое число")
                return v.toInt()
            }
            return TunnelConfig(
                mode = str("mode", "client"), address = str("address", ""), tun = str("tun", "frdptun0"),
                cidr = str("cidr", ""), mtu = int("mtu", 1280), bridge = str("bridge", ""),
                certificate = str("certificate", ""), privateKey = str("private_key", ""),
                serverSha256 = str("server_sha256", ""), uuid = str("uuid", ""), ipv4 = str("ipv4", ""),
                totalUploadBps = lng("total_upload_bps", 0), totalDownloadBps = lng("total_download_bps", 0),
                maxConnections = int("max_connections", 32),
                handshakeTimeoutSeconds = int("handshake_timeout_seconds", 20),
                idleTimeoutSeconds = int("idle_timeout_seconds", 60),
                queuePackets = int("queue_packets", 256),
            )
        }
    }
}
