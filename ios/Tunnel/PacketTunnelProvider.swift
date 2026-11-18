import Darwin
import Foundation
import NetworkExtension

/// Packet Tunnel: IPv4 packets from iOS <-> Go client (libfrtcore) <-> FreeRDP DVC.
final class PacketTunnelProvider: NEPacketTunnelProvider {
    private let logPath = (NSTemporaryDirectory() as NSString).appendingPathComponent("frt.log")
    private var running = false
    private var pumpStarted = false

    // MARK: lifecycle

    override func startTunnel(options: [String: NSObject]?, completionHandler: @escaping (Error?) -> Void) {
        redirectStderr()
        frt_register_bridge()

        guard let proto = protocolConfiguration as? NETunnelProviderProtocol,
              let pc = proto.providerConfiguration,
              let json = pc["config"] as? String,
              let cfg = try? TunnelConfig.parse(json) else {
            finishStart(error: "Не удалось прочитать конфигурацию", completion: completionHandler)
            return
        }
        let dns = ((pc["dns"] as? String) ?? "")
            .split(whereSeparator: { ", ;\n".contains($0) }).map(String.init)
            .filter { TunnelConfig.parseIPv4($0) != nil }
        let blockIPv6 = (pc["blockIPv6"] as? Bool) ?? true
        log("startTunnel \(cfg.address) cidr=\(cfg.cidr) mtu=\(cfg.mtu)")

        DispatchQueue.global(qos: .userInitiated).async { [self] in
            var err = [CChar](repeating: 0, count: 512)
            if FRTStart(json, &err, Int32(err.count)) != 0 {
                finishStart(error: String(cString: err), completion: completionHandler)
                return
            }
            running = true
            // Wait for RDP + UUID authentication.
            let deadline = Date().addingTimeInterval(Double(cfg.handshakeTimeoutSeconds) + 15)
            while true {
                let st = FRTStatus()
                if st == 2 { break }
                if st == 3 {
                    var b = [CChar](repeating: 0, count: 512)
                    FRTLastError(&b, Int32(b.count))
                    let m = String(cString: b)
                    finishStart(error: m.isEmpty ? "Соединение завершено" : m, completion: completionHandler)
                    return
                }
                if Date() > deadline {
                    FRTStop()
                    finishStart(error: "Таймаут подключения", completion: completionHandler)
                    return
                }
                usleep(100_000)
            }
            applySettings(cfg, dns: dns, blockIPv6: blockIPv6) { [self] error in
                if let error {
                    FRTStop()
                    finishStart(error: error.localizedDescription, completion: completionHandler)
                    return
                }
                log("tunnel is up")
                completionHandler(nil)
                startPump()
            }
        }
    }

    override func stopTunnel(with reason: NEProviderStopReason, completionHandler: @escaping () -> Void) {
        log("stopTunnel reason=\(reason.rawValue)")
        running = false
        DispatchQueue.global().async {
            FRTStop()
            completionHandler()
        }
    }

    override func handleAppMessage(_ messageData: Data, completionHandler: ((Data?) -> Void)?) {
        guard let completionHandler else { return }
        let data = (try? Data(contentsOf: URL(fileURLWithPath: logPath))) ?? Data()
        completionHandler(data.suffix(48 * 1024))
    }

    // MARK: settings

    private func applySettings(_ cfg: TunnelConfig, dns: [String], blockIPv6: Bool,
                               completion: @escaping (Error?) -> Void) {
        guard let c = cfg.cidrParts else { completion(NSError(domain: "FRT", code: 1)); return }
        let servers = resolveIPv4(cfg.host)
        let s = NEPacketTunnelNetworkSettings(tunnelRemoteAddress: servers.first ?? cfg.host)
        let v4 = NEIPv4Settings(addresses: [c.address], subnetMasks: [c.mask])
        // Everything goes into the tunnel; the server itself stays on the physical path.
        v4.includedRoutes = [NEIPv4Route.default()]
        v4.excludedRoutes = servers.prefix(8).map { NEIPv4Route(destinationAddress: $0, subnetMask: "255.255.255.255") }
        s.ipv4Settings = v4
        if blockIPv6 {
            // The tunnel is IPv4-only: capture IPv6 as well so it cannot leak (packets are dropped).
            let v6 = NEIPv6Settings(addresses: ["fd77:77::2"], networkPrefixLengths: [64])
            v6.includedRoutes = [NEIPv6Route.default()]
            s.ipv6Settings = v6
        }
        if !dns.isEmpty {
            let d = NEDNSSettings(servers: dns)
            d.matchDomains = [""]
            s.dnsSettings = d
        }
        s.mtu = NSNumber(value: cfg.mtu)
        setTunnelNetworkSettings(s, completionHandler: completion)
    }

    private func resolveIPv4(_ host: String) -> [String] {
        if TunnelConfig.parseIPv4(host) != nil { return [host] }
        var hints = addrinfo(ai_flags: 0, ai_family: AF_INET, ai_socktype: SOCK_STREAM, ai_protocol: 0,
                             ai_addrlen: 0, ai_canonname: nil, ai_addr: nil, ai_next: nil)
        var res: UnsafeMutablePointer<addrinfo>?
        guard getaddrinfo(host, nil, &hints, &res) == 0, let first = res else { return [] }
        defer { freeaddrinfo(res) }
        var out: [String] = []
        var p: UnsafeMutablePointer<addrinfo>? = first
        while let ai = p {
            if let sa = ai.pointee.ai_addr, sa.pointee.sa_family == sa_family_t(AF_INET) {
                let a = sa.withMemoryRebound(to: sockaddr_in.self, capacity: 1) { $0.pointee.sin_addr.s_addr }
                let v = UInt32(bigEndian: a)
                let str = TunnelConfig.formatIPv4(v)
                if !out.contains(str) { out.append(str) }
            }
            p = ai.pointee.ai_next
        }
        return out
    }

    // MARK: packet pump

    private func startPump() {
        guard !pumpStarted else { return }
        pumpStarted = true
        readLoop()
        let t = Thread { [weak self] in self?.writeLoop() }
        t.name = "frt.write"
        t.qualityOfService = .userInitiated
        t.start()
    }

    private func readLoop() {
        packetFlow.readPackets { [weak self] packets, protocols in
            guard let self, self.running else { return }
            for (i, p) in packets.enumerated() where protocols[i].int32Value == AF_INET {
                p.withUnsafeBytes { raw in
                    if let base = raw.bindMemory(to: UInt8.self).baseAddress {
                        FRTPushPacket(base, Int32(p.count))
                    }
                }
            }
            self.readLoop()
        }
    }

    private func writeLoop() {
        let cap = 65536
        let buf = UnsafeMutablePointer<UInt8>.allocate(capacity: cap)
        defer { buf.deallocate() }
        let ipv4 = NSNumber(value: AF_INET)
        var ended = false
        while running && !ended {
            var batch: [Data] = []
            var n = FRTPopPacket(buf, Int32(cap), 500)
            while n > 0 {
                batch.append(Data(bytes: buf, count: Int(n)))
                if batch.count >= 64 { break }
                n = FRTPopPacket(buf, Int32(cap), 0)
            }
            if n < 0 { ended = true }
            if !batch.isEmpty {
                packetFlow.writePackets(batch, withProtocols: Array(repeating: ipv4, count: batch.count))
            }
        }
        if running {
            running = false
            var b = [CChar](repeating: 0, count: 512)
            FRTLastError(&b, Int32(b.count))
            let m = String(cString: b)
            log("tunnel ended: \(m)")
            let err = NSError(domain: "FreeRDPTun", code: 2,
                              userInfo: [NSLocalizedDescriptionKey: m.isEmpty ? "Соединение закрыто сервером" : m])
            cancelTunnelWithError(err)
        }
    }

    // MARK: helpers

    private func finishStart(error message: String, completion: @escaping (Error?) -> Void) {
        log("start failed: \(message)")
        running = false
        // Give the app a moment to fetch the log before the extension exits.
        DispatchQueue.global().asyncAfter(deadline: .now() + 1.5) {
            completion(NSError(domain: "FreeRDPTun", code: 1,
                               userInfo: [NSLocalizedDescriptionKey: message]))
        }
    }

    private func redirectStderr() {
        let fd = open(logPath, O_WRONLY | O_CREAT | O_TRUNC, 0o600)
        if fd >= 0 {
            dup2(fd, 2)
            close(fd)
        }
        setvbuf(stderr, nil, _IONBF, 0)
    }

    private func log(_ s: String) {
        fputs("[swift] \(s)\n", stderr)
    }
}
