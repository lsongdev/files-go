import Foundation

/// A file as returned by the API (`server.View`). Works are files too, so every
/// list and detail screen uses this one type.
struct FileItem: Decodable, Identifiable, Hashable {
    let id: Int64
    let root: String
    let name: String
    let dir: Bool
    let size: Int64
    var kind: String?
    var meta: Meta?
    let version: Int64
    var thumb: Bool?
    var play: String?

    struct Meta: Decodable {
        var kind, title, show, artist, album, author, poster, artworkVersion, summary, video: String?
        var year, season, episode, tracks, width, height: Int?
        var duration, rating: Double?
        var genres: [String]?
        var info: [String: String]?
    }

    static func == (a: FileItem, b: FileItem) -> Bool { a.id == b.id && a.version == b.version }
    func hash(into hasher: inout Hasher) { hasher.combine(id) }

    /// Display rules match `title` and `subtitle` in web/app.js.
    var title: String {
        switch meta?.kind {
        case "episode": meta?.title ?? join(meta?.show, episodeCode)
        case "season": "第 \(meta?.season ?? 0) 季"
        default: meta?.title ?? name
        }
    }

    var subtitle: String {
        let m = meta
        if dir && m?.kind == nil { return "\(size) 项" }
        switch m?.kind {
        case "movie": return join(year, genres, rating, formatDuration(m?.duration))
        case "show": return join(year, genres, rating)
        case "season": return "\(size) 项"
        case "episode": return join(m?.title == nil ? nil : m?.show, episodeCode, formatDuration(m?.duration))
        case "video": return join(formatDuration(m?.duration), resolution, m?.video?.uppercased())
        case "audio": return join(m?.artist, m?.album, formatDuration(m?.duration))
        case "album": return join(m?.artist, year, m?.tracks.map { "\($0) 首" }, formatDuration(m?.duration))
        case "book", "document": return join(m?.author, m?.info?["Pages"].map { "\($0) 页" }, formatSize(size))
        default: return formatSize(size)
        }
    }

    /// Tall covers; everything else is square.
    var isPoster: Bool { ["movie", "show", "season", "book"].contains(meta?.kind ?? "") }

    private var year: String? { meta?.year.map(String.init) }
    private var genres: String? { meta?.genres.map { $0.prefix(2).joined(separator: " / ") } }
    private var rating: String? { meta?.rating.map { String(format: "★ %.1f", $0) } }
    private var episodeCode: String { String(format: "S%02dE%02d", meta?.season ?? 0, meta?.episode ?? 0) }
    private var resolution: String? {
        guard let w = meta?.width, let h = meta?.height, h > 0 else { return nil }
        let short = min(w, h)
        return short >= 2000 ? "4K" : short >= 1000 ? "1080p" : short >= 700 ? "720p" : "\(w)×\(h)"
    }
}

struct RootStatus: Decodable, Identifiable, Hashable {
    let id: String
    let name: String
    let fileId: Int64
    let state: String
}

struct Page: Decodable {
    let items: [FileItem]
    var more: Bool?
}

struct FileDetail: Decodable {
    let file: FileItem
}

struct Playback: Decodable {
    let url: String
}

private func join(_ parts: String?...) -> String {
    parts.compactMap { $0 }.filter { !$0.isEmpty }.joined(separator: " · ")
}

func formatDuration(_ seconds: Double?) -> String? {
    guard let seconds, seconds > 0 else { return nil }
    let s = Int(seconds.rounded())
    return s >= 3600
        ? String(format: "%d:%02d:%02d", s / 3600, s / 60 % 60, s % 60)
        : String(format: "%d:%02d", s / 60, s % 60)
}

func formatSize(_ bytes: Int64) -> String {
    bytes > 0 ? ByteCountFormatter.string(fromByteCount: bytes, countStyle: .binary) : "—"
}
