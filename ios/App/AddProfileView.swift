import SwiftUI

struct AddProfileView: View {
    @EnvironmentObject var store: ProfileStore
    @Environment(\.dismiss) private var dismiss
    @State private var name = ""
    @State private var text = ""
    @State private var error: String?

    var body: some View {
        NavigationStack {
            Form {
                Section("Название") {
                    TextField("Например, cdn1", text: $name)
                }
                Section {
                    TextEditor(text: $text)
                        .font(.system(.footnote, design: .monospaced))
                        .frame(minHeight: 280)
                        .autocorrectionDisabled()
                        .textInputAutocapitalization(.never)
                    Button("Вставить из буфера обмена") {
                        if let s = UIPasteboard.general.string { text = s }
                    }
                } header: {
                    Text("JSON-конфигурация клиента")
                } footer: {
                    Text("Позже здесь появится импорт по QR-коду.")
                }
                if let error {
                    Section { Text(error).foregroundStyle(.red).font(.footnote) }
                }
            }
            .navigationTitle("Новая конфигурация")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Отмена") { dismiss() } }
                ToolbarItem(placement: .confirmationAction) { Button("Добавить", action: add) }
            }
        }
    }

    private func add() {
        do {
            let cfg = try TunnelConfig.parse(text)
            let problems = cfg.validate()
            if !problems.isEmpty {
                error = problems.joined(separator: "\n")
                return
            }
            let n = name.trimmingCharacters(in: .whitespaces)
            store.add(Profile(name: n.isEmpty ? cfg.host : n, config: cfg))
            dismiss()
        } catch {
            self.error = error.localizedDescription
        }
    }
}
