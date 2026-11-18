import SwiftUI

@main
struct FreeRDPTunApp: App {
    @StateObject private var store = ProfileStore()
    @StateObject private var tunnel = TunnelController()

    var body: some Scene {
        WindowGroup {
            RootView()
                .environmentObject(store)
                .environmentObject(tunnel)
                .task { await tunnel.load() }
        }
    }
}
