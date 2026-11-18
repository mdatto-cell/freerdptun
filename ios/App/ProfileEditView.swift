import SwiftUI

/// All fields as strings so partially typed values are never rejected while editing.
struct ProfileDraft {
    var id: UUID
    var name: String
    var address: String, tun: String, cidr: String, mtu: String
    var bridge: String, certificate: String, privateKey: String
    var serverSHA256: String, uuid: String, ipv4: String
    var uploadBps: String, downloadBps: String
    var maxConnections: String, handshake: String, idle: String, queue: String
    var dns: String
    var blockIPv6: Bool

    init(_ p: Profile) {
        let c = p.config
        id = p.id; name = p.name
        address = c.address; tun = c.tun; cidr = c.cidr; mtu = String(c.mtu)
        bridge = c.bridge; certificate = c.certificate; privateKey = c.privateKey
        serverSHA256 = c.serverSHA256; uuid = c.uuid; ipv4 = c.ipv4
        uploadBps = String(c.totalUploadBps); downloadBps = String(c.totalDownloadBps)
        maxConnections = String(c.maxConnections); handshake = String(c.handshakeTimeoutSeconds)
        idle = String(c.idleTimeoutSeconds); queue = String(c.queuePackets)
        dns = p.dnsServers; blockIPv6 = p.blockIPv6
    }

    func build() throws -> Profile {
        func int(_ s: String, _ field: String) throws -> Int {
            guard let v = Int(s.trimmingCharacters(in: .whitespaces)) else {
                throw TunnelConfig.ParseError(message: "\(field): нужно целое число")
            }
            return v
        }
        func int64(_ s: String, _ field: String) throws -> Int64 {
            guard let v = Int64(s.trimmingCharacters(in: .whitespaces)) else {
                throw TunnelConfig.ParseError(message: "\(field): нужно целое число")
            }
            return v
        }
        func t(_ s: String) -> String { s.trimmingCharacters(in: .whitespacesAndNewlines) }
        var c = TunnelConfig()
        c.address = t(address); c.tun = t(tun); c.cidr = t(cidr); c.mtu = try int(mtu, "mtu")
        c.bridge = t(bridge); c.certificate = t(certificate); c.privateKey = t(privateKey)
        c.serverSHA256 = t(serverSHA256).lowercased(); c.uuid = t(uuid).lowercased(); c.ipv4 = t(ipv4)
        c.totalUploadBps = try int64(uploadBps, "total_upload_bps")
        c.totalDownloadBps = try int64(downloadBps, "total_download_bps")
        c.maxConnections = try int(maxConnections, "max_connections")
        c.handshakeTimeoutSeconds = try int(handshake, "handshake_timeout_seconds")
        c.idleTimeoutSeconds = try int(idle, "idle_timeout_seconds")
        c.queuePackets = try int(queue, "queue_packets")
        let problems = c.validate()
        if !problems.isEmpty { throw TunnelConfig.ParseError(message: problems.joined(separator: "\n")) }
        let n = t(name)
        return Profile(id: id, name: n.isEmpty ? c.host : n, config: c, dnsServers: t(dns), blockIPv6: blockIPv6)
    }
}

struct ProfileEditView: View {
    @EnvironmentObject var store: ProfileStore
    @EnvironmentObject var tunnel: TunnelController
    @Environment(\.dismiss) private var dismiss
    @State private var d: ProfileDraft
    @State private var error: String?

    init(profile: Profile) { _d = State(initialValue: ProfileDraft(profile)) }

    var body: some View {
        Form {
            Section("Название") { TextField("Название", text: $d.name) }
            Section("Подключение") {
                field("address", "host:порт", $d.address, keyboard: .URL)
                field("server_sha256", "64 hex-символа", $d.serverSHA256, mono: true)
                field("uuid", "8-4-4-4-12", $d.uuid, mono: true)
                LabeledContent("mode") { Text("client").foregroundStyle(.secondary) }
            }
            Section("Туннель") {
                field("tun", "frdptun0", $d.tun)
                field("cidr", "10.77.0.2/24", $d.cidr, keyboard: .numbersAndPunctuation)
                field("ipv4", "10.77.0.2", $d.ipv4, keyboard: .numbersAndPunctuation)
                field("mtu", "1280", $d.mtu, keyboard: .numberPad)
            }
            Section("Лимиты и таймауты") {
                field("total_upload_bps", "0 = без лимита", $d.uploadBps, keyboard: .numberPad)
                field("total_download_bps", "0 = без лимита", $d.downloadBps, keyboard: .numberPad)
                field("max_connections", "32", $d.maxConnections, keyboard: .numberPad)
                field("handshake_timeout_seconds", "20", $d.handshake, keyboard: .numberPad)
                field("idle_timeout_seconds", "60", $d.idle, keyboard: .numberPad)
                field("queue_packets", "256", $d.queue, keyboard: .numberPad)
            }
            Section {
                field("bridge", "не используется на iOS", $d.bridge)
                field("certificate", "только сервер", $d.certificate)
                field("private_key", "только сервер", $d.privateKey)
                LabeledContent("clients") { Text("null").foregroundStyle(.secondary) }
            } header: {
                Text("Прочие поля JSON")
            } footer: {
                Text("На iOS встроенный транспорт заменяет внешний bridge; клиенту эти поля не нужны.")
            }
            Section {
                field("DNS-серверы", "1.1.1.1, 8.8.8.8", $d.dns, keyboard: .numbersAndPunctuation)
                Toggle("Блокировать IPv6 (без утечек)", isOn: $d.blockIPv6)
            } header: {
                Text("Параметры приложения (вне JSON)")
            } footer: {
                Text("Туннель только IPv4. DNS-запросы уходят в туннель; сервер должен выполнять NAT и маршрутизацию.")
            }
            Section("JSON") {
                if let p = try? d.build() {
                    Text(p.config.jsonString())
                        .font(.system(.caption, design: .monospaced)).textSelection(.enabled)
                    Button("Скопировать JSON") { UIPasteboard.general.string = p.config.jsonString() }
                } else {
                    Text("Исправьте поля, чтобы увидеть JSON.").foregroundStyle(.secondary)
                }
            }
            if tunnel.isActive && tunnel.activeProfileID == d.id {
                Section { Text("Изменения применятся при следующем подключении.").font(.footnote) }
            }
            if let error {
                Section { Text(error).foregroundStyle(.red).font(.footnote) }
            }
        }
        .navigationTitle(d.name)
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            ToolbarItem(placement: .confirmationAction) { Button("Сохранить", action: save) }
        }
    }

    private func field(_ title: String, _ placeholder: String, _ binding: Binding<String>,
                       keyboard: UIKeyboardType = .default, mono: Bool = false) -> some View {
        VStack(alignment: .leading, spacing: 2) {
            Text(title).font(.caption).foregroundStyle(.secondary)
            TextField(placeholder, text: binding)
                .keyboardType(keyboard)
                .textInputAutocapitalization(.never)
                .autocorrectionDisabled()
                .font(mono ? .system(.footnote, design: .monospaced) : .body)
        }
    }

    private func save() {
        do {
            store.update(try d.build())
            dismiss()
        } catch {
            self.error = error.localizedDescription
        }
    }
}
