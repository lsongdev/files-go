import Foundation
import SwiftUI

enum APIError: LocalizedError {
    case unauthorized
    case status(Int, String)
    case invalidAddress

    var errorDescription: String? {
        switch self {
        case .unauthorized: "Incorrect username or password"
        case .status(let code, let message): message.isEmpty ? "Server returned status \(code)" : message
        case .invalidAddress: "Invalid server address"
        }
    }
}

/// Talks to one files-go server. Requests carry Basic Auth; HLS URLs do not
/// need it because their session key is the credential.
struct APIClient: Sendable {
    let baseURL: URL
    var authorization: String?

    private static let session: URLSession = {
        let config = URLSessionConfiguration.default
        config.urlCache = URLCache(memoryCapacity: 32 << 20, diskCapacity: 512 << 20)
        return URLSession(configuration: config)
    }()

    init(baseURL: URL, username: String = "", password: String = "") {
        self.baseURL = baseURL
        if !password.isEmpty {
            authorization = "Basic " + Data("\(username):\(password)".utf8).base64EncodedString()
        }
    }

    /// Accepts "192.168.8.100:8088" as well as a full URL.
    static func serverURL(from text: String) -> URL? {
        var text = text.trimmingCharacters(in: .whitespacesAndNewlines)
        while text.hasSuffix("/") { text.removeLast() }
        if !text.contains("://") { text = "http://" + text }
        guard let url = URL(string: text), url.host() != nil,
              url.scheme == "http" || url.scheme == "https" else { return nil }
        return url
    }

    func url(_ path: String, _ query: [String: String?] = [:]) -> URL {
        var components = URLComponents(url: baseURL, resolvingAgainstBaseURL: false)!
        components.path += path
        let items = query.compactMap { key, value in value.map { URLQueryItem(name: key, value: $0) } }
        if !items.isEmpty { components.queryItems = items.sorted { $0.name < $1.name } }
        return components.url!
    }

    func request(_ url: URL, method: String = "GET") -> URLRequest {
        var request = URLRequest(url: url)
        request.httpMethod = method
        request.setValue(authorization, forHTTPHeaderField: "Authorization")
        return request
    }

    private func check(_ response: URLResponse, _ body: Data = Data()) throws {
        guard let http = response as? HTTPURLResponse else { return }
        if http.statusCode == 401 { throw APIError.unauthorized }
        if http.statusCode >= 400 {
            struct Failure: Decodable { let error: String }
            let message = (try? JSONDecoder().decode(Failure.self, from: body))?.error ?? ""
            throw APIError.status(http.statusCode, message)
        }
    }

    func data(_ url: URL) async throws -> Data {
        let (data, response) = try await Self.session.data(for: request(url))
        try check(response, data)
        return data
    }

    private func get<T: Decodable>(_ path: String, _ query: [String: String?] = [:]) async throws -> T {
        try JSONDecoder().decode(T.self, from: await data(url(path, query)))
    }

    func roots() async throws -> [RootStatus] { try await get("/api/roots") }

    func file(_ id: Int64) async throws -> FileItem {
        let detail: FileDetail = try await get("/api/files/\(id)")
        return detail.file
    }

    func children(_ id: Int64, offset: Int, limit: Int) async throws -> Page {
        try await get("/api/files/\(id)/children", ["offset": String(offset), "limit": String(limit)])
    }

    func library(kind: String, offset: Int, limit: Int) async throws -> Page {
        try await get("/api/library", ["kind": kind, "offset": String(offset), "limit": String(limit)])
    }

    func search(_ query: String) async throws -> Page {
        try await get("/api/search", ["q": query])
    }

    /// The cache key changes with the artwork, as in the web client.
    func thumbURL(_ file: FileItem) -> URL {
        url("/api/files/\(file.id)/thumb", ["v": "\(file.version)-\(file.meta?.artworkVersion ?? "")-\(file.meta?.poster ?? "")"])
    }

    func backdropURL(_ file: FileItem) -> URL {
        url("/api/files/\(file.id)/backdrop", ["v": "\(file.version)-\(file.meta?.artworkVersion ?? "")"])
    }

    func play(_ id: Int64, transcode: Bool) async throws -> URL {
        let playback: Playback = try await get("/api/files/\(id)/play", ["transcode": transcode ? "1" : nil])
        guard let url = URL(string: playback.url, relativeTo: baseURL)?.absoluteURL else { throw APIError.invalidAddress }
        return url
    }

    /// Ends a playback session; the server also expires idle ones.
    func release(playback url: URL) async {
        let parts = url.pathComponents
        guard let index = parts.lastIndex(of: "hls"), index + 1 < parts.count else { return }
        _ = try? await Self.session.data(for: request(self.url("/api/hls/\(parts[index + 1])"), method: "DELETE"))
    }

    /// Saves a file under its own name in a temporary directory.
    func download(_ file: FileItem) async throws -> URL {
        let (temp, response) = try await Self.session.download(for: request(url("/api/files/\(file.id)/raw", ["download": ""])))
        try check(response)
        let dir = FileManager.default.temporaryDirectory.appending(path: "downloads/\(file.id)", directoryHint: .isDirectory)
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        let dest = dir.appending(path: file.name, directoryHint: .notDirectory)
        try? FileManager.default.removeItem(at: dest)
        try FileManager.default.moveItem(at: temp, to: dest)
        return dest
    }
}

extension EnvironmentValues {
    @Entry var api = APIClient(baseURL: URL(string: "http://localhost")!)
}
