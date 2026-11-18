import Foundation

/// Client configuration, 1:1 with the Go `tunnel.Config` JSON (client mode).
struct TunnelConfig: Codable, Equatable {
    var mode = "client"
    var address = ""
    var tun = "frdptun0"
    var cidr = ""
    var mtu = 1280
    var bridge = ""
    var certificate = ""
    var privateKey = ""
    var serverSHA256 = ""
    var uuid = ""
    var ipv4 = ""
    var totalUploadBps: Int64 = 0
    var totalDownloadBps: Int64 = 0
    var maxConnections = 32
    var handshakeTimeoutSeconds = 20
    var idleTimeoutSeconds = 60
    var queuePackets = 256

    enum CodingKeys: String, CodingKey {
        case mode, address, tun, cidr, mtu, bridge, certificate
        case privateKey = "private_key"
        case serverSHA256 = "server_sha256"
        case uuid, ipv4
        case totalUploadBps = "total_upload_bps"
        case totalDownloadBps = "total_download_bps"
        case maxConnections = "max_connections"
        case handshakeTimeoutSeconds = "handshake_timeout_seconds"
        case idleTimeoutSeconds = "idle_timeout_seconds"
        case queuePackets = "queue_packets"
        case clients
    }

    init() {}

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        mode = try c.decodeIfPresent(String.self, forKey: .mode) ?? "client"
        address = try c.decodeIfPresent(String.self, forKey: .address) ?? ""
        tun = try c.decodeIfPresent(String.self, forKey: .tun) ?? "frdptun0"
        cidr = try c.decodeIfPresent(String.self, forKey: .cidr) ?? ""
        mtu = try c.decodeIfPresent(Int.self, forKey: .mtu) ?? 1280
        bridge = try c.decodeIfPresent(String.self, forKey: .bridge) ?? ""
        certificate = try c.decodeIfPresent(String.self, forKey: .certificate) ?? ""
        privateKey = try c.decodeIfPresent(String.self, forKey: .privateKey) ?? ""
        serverSHA256 = try c.decodeIfPresent(String.self, forKey: .serverSHA256) ?? ""
        uuid = try c.decodeIfPresent(String.self, forKey: .uuid) ?? ""
        ipv4 = try c.decodeIfPresent(String.self, forKey: .ipv4) ?? ""
        totalUploadBps = try c.decodeIfPresent(Int64.self, forKey: .totalUploadBps) ?? 0
        totalDownloadBps = try c.decodeIfPresent(Int64.self, forKey: .totalDownloadBps) ?? 0
        maxConnections = try c.decodeIfPresent(Int.self, forKey: .maxConnections) ?? 32
        handshakeTimeoutSeconds = try c.decodeIfPresent(Int.self, forKey: .handshakeTimeoutSeconds) ?? 20
        idleTimeoutSeconds = try c.decodeIfPresent(Int.self, forKey: .idleTimeoutSeconds) ?? 60
        queuePackets = try c.decodeIfPresent(Int.self, forKey: .queuePackets) ?? 256
    }

    func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(mode, forKey: .mode)
        try c.encode(address, forKey: .address)
        try c.encode(tun, forKey: .tun)
        try c.encode(cidr, forKey: .cidr)
        try c.encode(mtu, forKey: .mtu)
        try c.encode(bridge, forKey: .bridge)
        try c.encode(certificate, forKey: .certificate)
        try c.encode(privateKey, forKey: .privateKey)
        try c.encode(serverSHA256, forKey: .serverSHA256)
        try c.encode(uuid, forKey: .uuid)
        try c.encode(ipv4, forKey: .ipv4)
        try c.encode(totalUploadBps, forKey: .totalUploadBps)
        try c.encode(totalDownloadBps, forKey: .totalDownloadBps)
        try c.encode(maxConnections, forKey: .maxConnections)
        try c.encode(handshakeTimeoutSeconds, forKey: .handshakeTimeoutSeconds)
        try c.encode(idleTimeoutSeconds, forKey: .idleTimeoutSeconds)
        try c.encode(queuePackets, forKey: .queuePackets)
        try c.encodeNil(forKey: .clients)
    }

    // MARK: JSON

    static let knownKeys: Set<String> = [
        "mode", "address", "tun", "cidr", "mtu", "bridge", "certificate", "private_key",
        "server_sha256", "uuid", "ipv4", "total_upload_bps", "total_download_bps",
        "max_connections", "handshake_timeout_seconds", "idle_timeout_seconds",
        "queue_packets", "clients",
    ]

    struct ParseError: LocalizedError {
        let message: String
        var errorDescription: String? { message }
    }

    /// Parses pasted text. Unknown keys are rejected, like the Go client does.
    static func parse(_ text: String) throws -> TunnelConfig {
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let data = trimmed.data(using: .utf8), !trimmed.isEmpty else {
            throw ParseError(message: "Пустая конфигурация")
        }
        let object: Any
        do { object = try JSONSerialization.jsonObject(with: data) } catch {
            throw ParseError(message: "Некорректный JSON: \(error.localizedDescription)")
        }
        guard let dict = object as? [String: Any] else {
            throw ParseError(message: "Ожидался JSON-объект")
        }
        let unknown = Set(dict.keys).subtracting(knownKeys)
        if !unknown.isEmpty {
            throw ParseError(message: "Неизвестные поля (в клиенте не поддерживаются): "
                + unknown.sorted().joined(separator: ", "))
        }
        if let clients = dict["clients"], !(clients is NSNull) {
            if let arr = clients as? [Any], arr.isEmpty {} else {
                throw ParseError(message: "Поле clients относится только к серверу")
            }
        }
        do { return try JSONDecoder().decode(TunnelConfig.self, from: data) } catch {
            throw ParseError(message: "Неверный тип поля: \(error.localizedDescription)")
        }
    }

    private static func q(_ s: String) -> String {
        guard let d = try? JSONEncoder().encode(s), let r = String(data: d, encoding: .utf8) else { return "\"\"" }
        return r.replacingOccurrences(of: "\\/", with: "/")
    }

    /// Pretty JSON in the same field order as the documented example.
    func jsonString() -> String {
        let lines = [
            "\"mode\": \(Self.q(mode))", "\"address\": \(Self.q(address))", "\"tun\": \(Self.q(tun))",
            "\"cidr\": \(Self.q(cidr))", "\"mtu\": \(mtu)", "\"bridge\": \(Self.q(bridge))",
            "\"certificate\": \(Self.q(certificate))", "\"private_key\": \(Self.q(privateKey))",
            "\"server_sha256\": \(Self.q(serverSHA256))", "\"uuid\": \(Self.q(uuid))",
            "\"ipv4\": \(Self.q(ipv4))", "\"total_upload_bps\": \(totalUploadBps)",
            "\"total_download_bps\": \(totalDownloadBps)", "\"max_connections\": \(maxConnections)",
            "\"handshake_timeout_seconds\": \(handshakeTimeoutSeconds)",
            "\"idle_timeout_seconds\": \(idleTimeoutSeconds)", "\"queue_packets\": \(queuePackets)",
            "\"clients\": null",
        ]
        return "{\n" + lines.map { "  " + $0 }.joined(separator: ",\n") + "\n}"
    }

    // MARK: Derived values

    var host: String {
        guard let i = address.lastIndex(of: ":") else { return address }
        return String(address[..<i]).trimmingCharacters(in: CharacterSet(charactersIn: "[]"))
    }
    var port: Int? {
        guard let i = address.lastIndex(of: ":") else { return nil }
        return Int(address[address.index(after: i)...])
    }

    static func parseIPv4(_ s: String) -> UInt32? {
        let parts = s.split(separator: ".", omittingEmptySubsequences: false)
        guard parts.count == 4 else { return nil }
        var v: UInt32 = 0
        for p in parts {
            guard !p.isEmpty, p.count <= 3, p.allSatisfy({ $0.isASCII && $0.isNumber }),
                  let n = UInt32(p), n <= 255 else { return nil }
            v = v << 8 | n
        }
        return v
    }
    static func formatIPv4(_ v: UInt32) -> String {
        "\(v >> 24 & 255).\(v >> 16 & 255).\(v >> 8 & 255).\(v & 255)"
    }

    /// (address, dotted mask, prefix length) from `cidr`.
    var cidrParts: (address: String, mask: String, bits: Int)? {
        let p = cidr.split(separator: "/")
        guard p.count == 2, let bits = Int(p[1]), (0...32).contains(bits),
              Self.parseIPv4(String(p[0])) != nil else { return nil }
        let m: UInt32 = bits == 0 ? 0 : UInt32.max << UInt32(32 - bits)
        return (String(p[0]), Self.formatIPv4(m), bits)
    }

    // MARK: Validation (mirrors tunnel.Config.Validate for mode=client)

    func validate() -> [String] {
        var e: [String] = []
        if mode != "client" { e.append("mode: должен быть client") }
        if host.isEmpty || port == nil || !(1...65535).contains(port!) {
            e.append("address: нужен формат host:порт (порт 1..65535)")
        }
        if tun.isEmpty || tun.count > 15 || tun.contains(where: { " /\\\0".contains($0) }) {
            e.append("tun: имя 1..15 символов без пробелов и / \\")
        }
        var cidrAddr: UInt32?
        if let c = cidrParts, let a = Self.parseIPv4(c.address) {
            cidrAddr = a
            let mask: UInt32 = c.bits == 0 ? 0 : UInt32.max << UInt32(32 - c.bits)
            if c.bits <= 30 && (a & ~mask == 0 || a & ~mask == ~mask) {
                e.append("cidr: нужен адрес хоста, а не сети/broadcast")
            }
        } else {
            e.append("cidr: нужен IPv4 CIDR, например 10.77.0.2/24")
        }
        if !(576...9000).contains(mtu) { e.append("mtu: 576..9000") }
        if !(3...120).contains(handshakeTimeoutSeconds) { e.append("handshake_timeout_seconds: 3..120") }
        if !(15...3600).contains(idleTimeoutSeconds) { e.append("idle_timeout_seconds: 15..3600") }
        if !(1...256).contains(maxConnections) { e.append("max_connections: 1..256") }
        if !(16...4096).contains(queuePackets) { e.append("queue_packets: 16..4096") }
        let maxRate: Int64 = 1_000_000_000_000
        if !(0...maxRate).contains(totalUploadBps) || !(0...maxRate).contains(totalDownloadBps) {
            e.append("total_upload_bps / total_download_bps: 0..1000000000000")
        }
        let u = uuid
        let uuidChars = u.replacingOccurrences(of: "-", with: "")
        let dashes = [8, 13, 18, 23].allSatisfy { i in
            u.count == 36 && u[u.index(u.startIndex, offsetBy: i)] == "-"
        }
        if u.count != 36 || !dashes || uuidChars.count != 32 || !uuidChars.allSatisfy({ $0.isHexDigit })
            || uuidChars.allSatisfy({ $0 == "0" }) {
            e.append("uuid: формат 8-4-4-4-12, не нулевой")
        }
        if let ip = Self.parseIPv4(ipv4) {
            if let a = cidrAddr, a != ip { e.append("ipv4: должен совпадать с адресом в cidr") }
        } else {
            e.append("ipv4: нужен IPv4-адрес")
        }
        if serverSHA256.count != 64 || !serverSHA256.allSatisfy({ $0.isHexDigit }) {
            e.append("server_sha256: 64 hex-символа (SHA-256 DER листового сертификата)")
        }
        return e
    }
}
