import SwiftUI

struct LogView: View {
    @EnvironmentObject var tunnel: TunnelController
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            ScrollView {
                Text(tunnel.log.isEmpty ? "Журнал пуст. Он доступен во время подключения." : tunnel.log)
                    .font(.system(.caption2, design: .monospaced))
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding()
                    .textSelection(.enabled)
            }
            .navigationTitle("Журнал")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Закрыть") { dismiss() } }
                ToolbarItem(placement: .primaryAction) {
                    Button { UIPasteboard.general.string = tunnel.log } label: {
                        Image(systemName: "doc.on.doc")
                    }
                }
            }
            .onAppear { tunnel.fetchLog() }
        }
    }
}
