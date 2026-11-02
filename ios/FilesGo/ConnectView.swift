import SwiftUI

struct ConnectView: View {
    @Environment(Session.self) private var session
    @State private var address = ""
    @State private var username = ""
    @State private var password = ""
    @State private var connecting = false
    @State private var error: String?

    var body: some View {
        NavigationStack {
            Form {
                Section("Server") {
                    TextField("192.168.8.100:8088", text: $address)
                        .keyboardType(.URL)
                        .textContentType(.URL)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                }
                Section {
                    TextField("Username", text: $username)
                        .textContentType(.username)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                    SecureField("Password", text: $password).textContentType(.password)
                } header: {
                    Text("Account")
                } footer: {
                    Text("Leave blank if the server does not require authentication.")
                }
                if let error {
                    Text(error).foregroundStyle(.red)
                }
                Button {
                    connect()
                } label: {
                    HStack {
                        Text("Connect")
                        if connecting { Spacer(); ProgressView() }
                    }
                }
                .disabled(connecting || address.isEmpty)
            }
            .navigationTitle("Connect to Server")
            .onAppear {
                address = session.address
                username = session.username
            }
        }
    }

    private func connect() {
        connecting = true
        error = nil
        Task {
            do {
                try await session.connect(address: address, username: username, password: password)
            } catch {
                self.error = error.localizedDescription
            }
            connecting = false
        }
    }
}

struct SettingsView: View {
    @Environment(Session.self) private var session
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            Form {
                LabeledContent("Server", value: session.address)
                if !session.username.isEmpty {
                    LabeledContent("Username", value: session.username)
                }
                Button("Disconnect", role: .destructive) { session.disconnect() }
            }
            .navigationTitle("Settings")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .confirmationAction) { Button("Done") { dismiss() } }
            }
        }
    }
}
