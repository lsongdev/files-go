import Foundation
import Observation
import Security

/// The connected server. The address and username live in UserDefaults, the
/// password in the Keychain. One server at a time: connecting elsewhere drops
/// everything cached for the previous one.
@Observable
final class Session {
    private(set) var client: APIClient?
    var username = UserDefaults.standard.string(forKey: "username") ?? ""
    var address = UserDefaults.standard.string(forKey: "serverURL") ?? ""

    init() {
        if let url = APIClient.serverURL(from: address) {
            client = APIClient(baseURL: url, username: username, password: Keychain.password ?? "")
        }
    }

    func connect(address: String, username: String, password: String) async throws {
        guard let url = APIClient.serverURL(from: address) else { throw APIError.invalidAddress }
        let client = APIClient(baseURL: url, username: username, password: password)
        _ = try await client.roots()
        UserDefaults.standard.set(url.absoluteString, forKey: "serverURL")
        UserDefaults.standard.set(username, forKey: "username")
        Keychain.password = password
        self.address = url.absoluteString
        self.username = username
        URLCache.shared.removeAllCachedResponses()
        self.client = client
    }

    func disconnect() {
        UserDefaults.standard.removeObject(forKey: "serverURL")
        Keychain.password = nil
        address = ""
        client = nil
    }
}

enum Keychain {
    private static let query: [String: Any] = [kSecClass as String: kSecClassGenericPassword, kSecAttrAccount as String: "server"]

    static var password: String? {
        get {
            var result: AnyObject?
            var search = query
            search[kSecReturnData as String] = true
            guard SecItemCopyMatching(search as CFDictionary, &result) == errSecSuccess, let data = result as? Data else { return nil }
            return String(data: data, encoding: .utf8)
        }
        set {
            SecItemDelete(query as CFDictionary)
            guard let newValue, !newValue.isEmpty else { return }
            var item = query
            item[kSecValueData as String] = Data(newValue.utf8)
            item[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlock
            SecItemAdd(item as CFDictionary, nil)
        }
    }
}
