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
                Section("服务器") {
                    TextField("192.168.8.100:8088", text: $address)
                        .keyboardType(.URL)
                        .textContentType(.URL)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                }
                Section {
                    TextField("用户名", text: $username)
                        .textContentType(.username)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                    SecureField("密码", text: $password).textContentType(.password)
                } header: {
                    Text("账号")
                } footer: {
                    Text("服务器未开启认证时可留空。")
                }
                if let error {
                    Text(error).foregroundStyle(.red)
                }
                Button {
                    connect()
                } label: {
                    HStack {
                        Text("连接")
                        if connecting { Spacer(); ProgressView() }
                    }
                }
                .disabled(connecting || address.isEmpty)
            }
            .navigationTitle("连接服务器")
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
                LabeledContent("服务器", value: session.address)
                if !session.username.isEmpty {
                    LabeledContent("用户名", value: session.username)
                }
                Button("断开连接", role: .destructive) { session.disconnect() }
            }
            .navigationTitle("设置")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .confirmationAction) { Button("完成") { dismiss() } }
            }
        }
    }
}
