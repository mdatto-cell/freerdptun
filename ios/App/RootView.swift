import SwiftUI

struct RootView: View {
    @EnvironmentObject var store: ProfileStore
    @EnvironmentObject var tunnel: TunnelController
    @State private var showAdd = false
    @State private var showLog = false
    @State private var pendingDelete: Profile?

    var body: some View {
        NavigationStack {
            List {
                Section { StatusCard() }
                Section("Конфигурации") {
                    if store.profiles.isEmpty {
                        Text("Нет конфигураций. Нажмите «+» и вставьте JSON.")
                            .foregroundStyle(.secondary)
                    }
                    ForEach(store.profiles) { p in
                        NavigationLink {
                            ProfileEditView(profile: p)
                        } label: {
                            ProfileRow(profile: p,
                                       isDefault: store.defaultID == p.id,
                                       isActive: tunnel.isActive && tunnel.activeProfileID == p.id)
                        }
                        .swipeActions(edge: .trailing) {
                            Button(role: .destructive) { requestDelete(p) } label: {
                                Label("Удалить", systemImage: "trash")
                            }
                        }
                        .swipeActions(edge: .leading) {
                            Button { store.setDefault(p.id) } label: {
                                Label("По умолчанию", systemImage: "star")
                            }.tint(.yellow)
                        }
                        .contextMenu {
                            Button { store.setDefault(p.id) } label: {
                                Label("Сделать по умолчанию", systemImage: "star")
                            }
                            Button(role: .destructive) { requestDelete(p) } label: {
                                Label("Удалить", systemImage: "trash")
                            }
                        }
                    }
                }
            }
            .navigationTitle("FreeRDPTun")
            .toolbar {
                ToolbarItem(placement: .topBarLeading) {
                    Button { showLog = true } label: { Image(systemName: "doc.text") }
                }
                ToolbarItem(placement: .topBarTrailing) {
                    Button { showAdd = true } label: { Image(systemName: "plus") }
                }
            }
            .sheet(isPresented: $showAdd) { AddProfileView() }
            .sheet(isPresented: $showLog) { LogView() }
            .confirmationDialog("Удалить конфигурацию?", isPresented: Binding(
                get: { pendingDelete != nil }, set: { if !$0 { pendingDelete = nil } }),
                titleVisibility: .visible, presenting: pendingDelete) { p in
                Button("Удалить «\(p.name)»", role: .destructive) { store.delete(p.id) }
            }
        }
    }

    private func requestDelete(_ p: Profile) {
        if tunnel.isActive && tunnel.activeProfileID == p.id {
            tunnel.lastError = "Сначала отключитесь, затем удаляйте активную конфигурацию."
        } else {
            pendingDelete = p
        }
    }
}

struct ProfileRow: View {
    let profile: Profile
    let isDefault: Bool
    let isActive: Bool

    var body: some View {
        HStack {
            VStack(alignment: .leading, spacing: 2) {
                Text(profile.name).font(.headline)
                Text(profile.config.address).font(.footnote).foregroundStyle(.secondary)
                Text("\(profile.config.cidr) · MTU \(profile.config.mtu)")
                    .font(.caption).foregroundStyle(.secondary)
            }
            Spacer()
            if isActive { Image(systemName: "bolt.fill").foregroundStyle(.green) }
            if isDefault { Image(systemName: "star.fill").foregroundStyle(.yellow) }
        }
    }
}

struct StatusCard: View {
    @EnvironmentObject var store: ProfileStore
    @EnvironmentObject var tunnel: TunnelController

    private var busy: Bool { tunnel.status == .connecting || tunnel.status == .disconnecting }

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack {
                Circle().fill(color).frame(width: 12, height: 12)
                Text(tunnel.statusText).font(.title3.bold())
                if busy { ProgressView().padding(.leading, 4) }
            }
            if let p = store.defaultProfile {
                let shown = tunnel.isActive
                    ? (store.profiles.first { $0.id == tunnel.activeProfileID } ?? p) : p
                Text("\(shown.name) — \(shown.config.address)")
                    .font(.subheadline).foregroundStyle(.secondary)
            } else {
                Text("Нет конфигурации по умолчанию").font(.subheadline).foregroundStyle(.secondary)
            }
            if let e = tunnel.lastError {
                Text(e).font(.footnote).foregroundStyle(.red).textSelection(.enabled)
            }
            Button {
                if tunnel.isActive {
                    tunnel.disconnect()
                } else if let p = store.defaultProfile {
                    Task { await tunnel.connect(p) }
                }
            } label: {
                Text(tunnel.isActive ? "Отключиться" : "Подключиться")
                    .frame(maxWidth: .infinity).padding(.vertical, 6)
            }
            .buttonStyle(.borderedProminent)
            .tint(tunnel.isActive ? .red : .accentColor)
            .disabled(tunnel.status == .disconnecting || (!tunnel.isActive && store.defaultProfile == nil))
        }
        .padding(.vertical, 4)
    }

    private var color: Color {
        switch tunnel.status {
        case .connected: return .green
        case .connecting, .reasserting, .disconnecting: return .orange
        default: return .gray
        }
    }
}
