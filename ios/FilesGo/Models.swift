import CoreGraphics
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
        var kind, title, original, show, artist, album, author, poster, backdrop, artworkVersion, summary, video, audio, container: String?
        var year, season, episode, tracks, albums, width, height: Int?
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
        case "season": "Season \(meta?.season ?? 0)"
        default: meta?.title ?? name
        }
    }

    var subtitle: String {
        let m = meta
        if dir && m?.kind == nil { return "\(size) \(size == 1 ? "item" : "items")" }
        switch m?.kind {
        case "movie": return join(year, genres, rating, formatDuration(m?.duration))
        case "show": return join(year, genres, rating)
        case "season": return "\(size) \(size == 1 ? "item" : "items")"
        case "episode": return join(m?.title == nil ? nil : m?.show, episodeCode, formatDuration(m?.duration))
        case "video": return join(formatDuration(m?.duration), resolution, m?.video?.uppercased())
        case "audio": return join(m?.artist, m?.album, formatDuration(m?.duration))
        case "album": return join(m?.artist, year, m?.tracks.map { "\($0) \($0 == 1 ? "track" : "tracks")" }, formatDuration(m?.duration))
        case "book", "document": return join(m?.author, m?.info?["Pages"].map { "\($0) \($0 == "1" ? "page" : "pages")" }, formatSize(size))
        default: return formatSize(size)
        }
    }

    /// Width over height of the cover, as in the web client.
    var coverAspect: CGFloat {
        if ["movie", "show", "season", "book"].contains(meta?.kind ?? "") { return 2.0 / 3.0 }
        return meta?.kind == "album" || kind == "audio" || kind == "image" ? 1 : 16.0 / 10.0
    }

    /// A folder with no identity is just a list; everything else gets a header.
    var hasHero: Bool { !dir || meta?.kind != nil }

    var kindLabel: String? { Self.kindLabels[meta?.kind ?? kind ?? ""] }

    /// The details table, following `Details` in web/app.js.
    var infoRows: [(label: String, value: String)] {
        guard let m = meta else { return dir ? [] : [("Size", formatSize(size))] }
        var rows: [(String, String?)] = [
            ("Size", dir ? nil : formatSize(size)),
            ("Original Title", m.original), ("Author", m.author), ("Artist", m.artist), ("Album", m.album),
            ("Tracks", m.tracks.map(String.init)), ("Albums", m.albums.map(String.init)),
            ("Duration", formatDuration(m.duration)),
            ("Resolution", m.width.flatMap { w in m.height.map { "\(w)×\($0)" } }),
            ("Codecs", [m.video, m.audio, m.container].compactMap { $0 }.joined(separator: " · ")),
            ("Genres", m.genres?.joined(separator: " / ")),
        ]
        for (key, value) in (m.info ?? [:]).sorted(by: { $0.key < $1.key }) {
            rows.append((key, value))
        }
        return rows.compactMap { row in row.1.flatMap { $0.isEmpty ? nil : (row.0, $0) } }
    }

    private static let kindLabels = ["movie": "Movie", "show": "TV Show", "season": "Season", "episode": "Episode", "video": "Video", "audio": "Audio", "album": "Album", "photo": "Photo", "image": "Image", "book": "Book", "document": "Document", "app": "App", "archive": "Archive", "text": "Text", "code": "Code", "font": "Font", "torrent": "Torrent", "folder": "Folder"]

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

extension FileItem {
    /// A root's top directory, which the roots list already identifies.
    init(root: RootStatus) {
        self.init(id: root.fileId, root: root.id, name: root.name, dir: true, size: 0, version: 0)
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
    var more: Bool? = nil
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
