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
        guard let m = meta else { return dir ? [] : [("大小", formatSize(size))] }
        var rows: [(String, String?)] = [
            ("大小", dir ? nil : formatSize(size)),
            ("原名", m.original), ("作者", m.author), ("艺术家", m.artist), ("专辑", m.album),
            ("曲目数", m.tracks.map(String.init)), ("专辑数", m.albums.map(String.init)),
            ("时长", formatDuration(m.duration)),
            ("分辨率", m.width.flatMap { w in m.height.map { "\(w)×\($0)" } }),
            ("编码", [m.video, m.audio, m.container].compactMap { $0 }.joined(separator: " · ")),
            ("类型标签", m.genres?.joined(separator: " / ")),
        ]
        for (key, value) in (m.info ?? [:]).sorted(by: { $0.key < $1.key }) {
            rows.append((Self.infoLabels[key] ?? key, value))
        }
        return rows.compactMap { row in row.1.flatMap { $0.isEmpty ? nil : (row.0, $0) } }
    }

    private static let kindLabels = ["movie": "电影", "show": "剧集", "season": "季", "episode": "单集", "video": "视频", "audio": "音频", "album": "专辑", "photo": "照片", "image": "图片", "book": "图书", "document": "文档", "app": "应用", "archive": "压缩包", "text": "文本", "code": "代码", "font": "字体", "torrent": "种子", "folder": "文件夹"]
    private static let infoLabels = ["Release": "版本", "Bitrate": "码率", "Channels": "声道", "Sample rate": "采样率", "Subtitles": "字幕", "Track": "音轨", "Disc": "碟", "Composer": "作曲", "Format": "格式", "Taken": "拍摄时间", "Camera": "相机", "Lens": "镜头", "Exposure": "快门", "Aperture": "光圈", "ISO": "ISO", "Focal length": "焦距", "Location": "位置", "Publisher": "出版社", "Language": "语言", "Pages": "页数", "Slides": "幻灯片", "Application": "应用程序", "Subject": "主题", "Creator": "创建工具", "Producer": "生成工具", "Package": "包名", "Version": "版本", "Version code": "版本号", "Min SDK": "最低 SDK", "Target SDK": "目标 SDK", "Architecture": "架构", "Type": "类型", "Description": "描述", "File version": "文件版本", "Copyright": "版权", "Files": "文件数", "Uncompressed": "解压后", "Runtime": "片长", "Embedded title": "内嵌标题", "Code": "番号", "Studio": "片商", "Label": "厂牌", "Series": "系列", "Director": "导演", "Performers": "演员", "Released": "发行日期"]

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
