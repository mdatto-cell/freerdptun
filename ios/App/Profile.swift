import Foundation

struct Profile: Identifiable, Codable, Equatable {
    var id = UUID()
    var name: String
    var config: TunnelConfig
    /// App-level settings (not part of the JSON config).
    var dnsServers = "1.1.1.1, 8.8.8.8"
    var blockIPv6 = true
}

@MainActor
final class ProfileStore: ObservableObject {
    @Published private(set) var profiles: [Profile] = []
    @Published private(set) var defaultID: UUID?

    private let profilesKey = "profiles.v1"
    private let defaultKey = "defaultProfile.v1"

    init() {
        let d = UserDefaults.standard
        if let data = d.data(forKey: profilesKey),
           let list = try? JSONDecoder().decode([Profile].self, from: data) {
            profiles = list
        }
        if let s = d.string(forKey: defaultKey), let id = UUID(uuidString: s),
           profiles.contains(where: { $0.id == id }) {
            defaultID = id
        } else {
            defaultID = profiles.first?.id
        }
    }

    var defaultProfile: Profile? { profiles.first { $0.id == defaultID } }

    func add(_ p: Profile) {
        profiles.append(p)
        if defaultID == nil { defaultID = p.id }
        save()
    }

    func update(_ p: Profile) {
        guard let i = profiles.firstIndex(where: { $0.id == p.id }) else { return }
        profiles[i] = p
        save()
    }

    func delete(_ id: UUID) {
        profiles.removeAll { $0.id == id }
        if defaultID == id { defaultID = profiles.first?.id }
        save()
    }

    func setDefault(_ id: UUID) {
        guard profiles.contains(where: { $0.id == id }) else { return }
        defaultID = id
        save()
    }

    private func save() {
        let d = UserDefaults.standard
        d.set(try? JSONEncoder().encode(profiles), forKey: profilesKey)
        d.set(defaultID?.uuidString, forKey: defaultKey)
    }
}
