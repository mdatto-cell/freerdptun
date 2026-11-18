import Foundation
import NetworkExtension

@MainActor
final class TunnelController: ObservableObject {
    @Published private(set) var status: NEVPNStatus = .invalid
    @Published private(set) var activeProfileID: UUID?
    @Published var lastError: String?
    @Published private(set) var log = ""

    private var manager: NETunnelProviderManager?
    private var statusObserver: NSObjectProtocol?
    private var pollTimer: Timer?

    private var extensionBundleID: String {
        (Bundle.main.bundleIdentifier ?? "") + ".tunnel"
    }

    var isActive: Bool {
        switch status {
        case .connecting, .connected, .reasserting, .disconnecting: return true
        default: return false
        }
    }

    var statusText: String {
        switch status {
        case .invalid: return "Не настроено"
        case .disconnected: return "Отключено"
        case .connecting: return "Подключение…"
        case .connected: return "Подключено"
        case .reasserting: return "Переподключение…"
        case .disconnecting: return "Отключение…"
        @unknown default: return "—"
        }
    }

    // MARK: setup

    func load() async {
        do {
            let all = try await NETunnelProviderManager.loadAllFromPreferences()
            let mine = all.first {
                ($0.protocolConfiguration as? NETunnelProviderProtocol)?.providerBundleIdentifier == extensionBundleID
            }
            adopt(mine)
        } catch {
            lastError = "Не удалось загрузить настройки VPN: \(error.localizedDescription)"
        }
    }

    private func adopt(_ m: NETunnelProviderManager?) {
        manager = m
        if let o = statusObserver { NotificationCenter.default.removeObserver(o) }
        statusObserver = nil
        guard let m else { status = .invalid; return }
        status = m.connection.status
        syncActiveProfile()
        statusObserver = NotificationCenter.default.addObserver(
            forName: .NEVPNStatusDidChange, object: m.connection, queue: .main
        ) { [weak self] _ in
            Task { @MainActor in self?.statusChanged() }
        }
        statusChanged()
    }

    private func syncActiveProfile() {
        if let p = (manager?.protocolConfiguration as? NETunnelProviderProtocol)?.providerConfiguration,
           let s = p["profileID"] as? String {
            activeProfileID = UUID(uuidString: s)
        }
    }

    private func statusChanged() {
        guard let c = manager?.connection else { return }
        let old = status
        status = c.status
        switch status {
        case .connecting, .connected, .reasserting:
            startPolling()
        case .disconnected, .invalid:
            stopPolling()
            if old == .connecting || old == .connected || old == .reasserting || old == .disconnecting {
                c.fetchLastDisconnectError { [weak self] error in
                    guard let error else { return }
                    let msg = error.localizedDescription
                    Task { @MainActor in self?.lastError = msg }
                }
            }
        default: break
        }
    }

    // MARK: actions

    func connect(_ profile: Profile) async {
        lastError = nil
        log = ""
        let problems = profile.config.validate()
        if !problems.isEmpty {
            lastError = problems.joined(separator: "\n")
            return
        }
        do {
            let m = manager ?? NETunnelProviderManager()
            let proto = NETunnelProviderProtocol()
            proto.providerBundleIdentifier = extensionBundleID
            proto.serverAddress = profile.config.host
            proto.providerConfiguration = [
                "config": profile.config.jsonString(),
                "dns": profile.dnsServers,
                "blockIPv6": profile.blockIPv6,
                "profileID": profile.id.uuidString,
                "name": profile.name,
            ]
            m.protocolConfiguration = proto
            m.localizedDescription = "FreeRDPTun"
            m.isEnabled = true
            try await m.saveToPreferences()
            try await m.loadFromPreferences()
            adopt(m)
            activeProfileID = profile.id
            try (m.connection as? NETunnelProviderSession)?.startVPNTunnel()
        } catch {
            lastError = error.localizedDescription
        }
    }

    func disconnect() {
        manager?.connection.stopVPNTunnel()
    }

    // MARK: log

    private func startPolling() {
        guard pollTimer == nil else { return }
        fetchLog()
        pollTimer = Timer.scheduledTimer(withTimeInterval: 1.0, repeats: true) { [weak self] _ in
            Task { @MainActor in self?.fetchLog() }
        }
    }

    private func stopPolling() {
        pollTimer?.invalidate()
        pollTimer = nil
    }

    func fetchLog() {
        guard let s = manager?.connection as? NETunnelProviderSession, isActive else { return }
        try? s.sendProviderMessage(Data("log".utf8)) { [weak self] data in
            guard let data, let text = String(data: data, encoding: .utf8) else { return }
            Task { @MainActor in self?.log = text }
        }
    }
}
