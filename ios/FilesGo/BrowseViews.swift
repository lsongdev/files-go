import QuickLook
import SwiftUI

struct RemoteImage: View {
    let url: URL?
    var icon = "photo"
    @Environment(\.api) private var api
    @State private var image: UIImage?

    var body: some View {
        ZStack {
            Rectangle().fill(.quaternary)
            if let image {
                Image(uiImage: image).resizable().scaledToFill()
            } else if url == nil {
                Image(systemName: icon).foregroundStyle(.secondary)
            }
        }
        .clipped()
        .task(id: url) {
            guard let url, let data = try? await api.data(url) else { return }
            image = UIImage(data: data)
        }
    }
}

struct Cover: View {
    let file: FileItem
    @Environment(\.api) private var api

    var body: some View {
        Color.clear
            .aspectRatio(file.coverAspect, contentMode: .fit)
            .overlay { RemoteImage(url: file.thumb == true ? api.thumbURL(file) : nil, icon: icon) }
            .clipShape(.rect(cornerRadius: 8))
    }

    private var icon: String {
        if file.dir && file.meta?.kind != "album" { return "folder" }
        switch file.kind {
        case "video": return "film"
        case "audio", "folder": return "music.note"
        case "image": return "photo"
        case "book": return "book"
        default: return "doc"
        }
    }
}

struct FileRow: View {
    let file: FileItem

    var body: some View {
        NavigationLink(value: file) {
            HStack(spacing: 12) {
                Cover(file: file)
                    .frame(width: file.coverAspect > 1 ? 72 : 44)
                VStack(alignment: .leading, spacing: 2) {
                    Text(file.title).lineLimit(2).multilineTextAlignment(.leading)
                    Text(file.subtitle).font(.caption).foregroundStyle(.secondary).lineLimit(1)
                }
                Spacer(minLength: 8)
                Image(systemName: "chevron.right").font(.caption.weight(.semibold)).foregroundStyle(.tertiary)
            }
            .padding(.horizontal, 16)
            .padding(.vertical, 8)
            .contentShape(.rect)
        }
        .buttonStyle(.plain)
    }
}

/// Files tab: the configured roots, and search across all of them.
struct RootsView: View {
    @Environment(\.api) private var api
    @State private var roots: [RootStatus] = []
    @State private var error: String?
    @State private var showSettings = false
    @State private var query = ""
    @State private var results: [FileItem] = []

    var body: some View {
        ScrollView {
            LazyVStack(alignment: .leading, spacing: 0) {
                if query.isEmpty {
                    ForEach(roots) { root in
                        NavigationLink(value: FileItem(root: root)) {
                            HStack(spacing: 12) {
                                Image(systemName: "externaldrive").foregroundStyle(.secondary).frame(width: 44)
                                Text(root.name)
                                Spacer()
                                if root.state != "idle" {
                                    Text(stateLabel(root.state)).font(.caption).foregroundStyle(.secondary)
                                }
                                Image(systemName: "chevron.right").font(.caption.weight(.semibold)).foregroundStyle(.tertiary)
                            }
                            .padding(.horizontal, 16)
                            .padding(.vertical, 14)
                            .contentShape(.rect)
                        }
                        .buttonStyle(.plain)
                        Divider().padding(.leading, 72)
                    }
                } else {
                    ForEach(results) { file in
                        FileRow(file: file)
                        Divider().padding(.leading, 72)
                    }
                }
            }
        }
        .navigationTitle("文件")
        .searchable(text: $query, prompt: "搜索全部文件")
        .overlay {
            if let error, roots.isEmpty, query.isEmpty {
                ContentUnavailableView("加载失败", systemImage: "exclamationmark.triangle", description: Text(error))
            }
        }
        .toolbar {
            Button("设置", systemImage: "gearshape") { showSettings = true }
        }
        .sheet(isPresented: $showSettings) { SettingsView() }
        .task { await loadRoots() }
        .refreshable { await loadRoots() }
        .task(id: query) {
            guard !query.isEmpty else { results = []; return }
            try? await Task.sleep(for: .milliseconds(300))
            guard !Task.isCancelled, let page = try? await api.search(query), !Task.isCancelled else { return }
            results = page.items
        }
    }

    private func loadRoots() async {
        do {
            roots = try await api.roots()
            error = nil
        } catch {
            self.error = error.localizedDescription
        }
    }

    private func stateLabel(_ state: String) -> String {
        switch state {
        case "scanning": "扫描中"
        case "offline", "mount_changed": "离线"
        case "error": "错误"
        default: state
        }
    }
}

/// The one detail screen, laid out like the web client: artwork behind the
/// page, cover and heading, summary, the main action, then the contents and
/// details. A folder with no identity is only its contents.
struct ItemView: View {
    let file: FileItem
    @Environment(\.api) private var api
    @State private var pager = Pager()
    @State private var playing: FileItem?
    @State private var preview: URL?
    @State private var opening = false
    @State private var error: String?
    @State private var summaryExpanded = false

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 0) {
                if file.hasHero { hero }
                if file.dir { contents }
                if file.hasHero && !file.infoRows.isEmpty { details }
            }
        }
        .ignoresSafeArea(edges: file.hasHero ? .top : [])
        .navigationTitle(file.title)
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            if file.hasHero {
                ToolbarItem(placement: .principal) { Color.clear.frame(width: 1, height: 1) } // the hero carries the title
            }
        }
        .toolbarBackground(file.hasHero ? .hidden : .automatic, for: .navigationBar)
        .loads(pager) { [api, file] offset, limit in
            file.dir ? try await api.children(file.id, offset: offset, limit: limit) : Page(items: [])
        }
        .fullScreenCover(item: $playing) { PlayerView(file: $0) }
        .quickLookPreview($preview)
    }

    private var hero: some View {
        VStack(alignment: .leading, spacing: 14) {
            backdrop
            HStack(alignment: .bottom, spacing: 14) {
                if file.thumb == true {
                    Cover(file: file)
                        .frame(width: file.coverAspect > 1 ? 150 : 110)
                        .shadow(color: .black.opacity(0.3), radius: 8, y: 3)
                }
                VStack(alignment: .leading, spacing: 4) {
                    if let label = file.kindLabel {
                        Text(label).font(.caption).foregroundStyle(.secondary)
                    }
                    Text(file.title).font(.title2.bold()).lineLimit(4)
                    Text(file.subtitle).font(.subheadline).foregroundStyle(.secondary).lineLimit(3)
                }
            }
            .padding(.horizontal, 16)
            .padding(.top, file.thumb == true ? -96 : -40)
            if let summary = file.meta?.summary, !summary.isEmpty {
                Text(summary)
                    .font(.subheadline)
                    .lineSpacing(3)
                    .lineLimit(summaryExpanded ? nil : 5)
                    .contentShape(.rect)
                    .onTapGesture { summaryExpanded.toggle() }
                    .padding(.horizontal, 16)
            }
            action.padding(.horizontal, 16)
            if let error {
                Text(error).font(.caption).foregroundStyle(.red).padding(.horizontal, 16)
            }
        }
        .padding(.bottom, 12)
    }

    /// The backdrop, or the blurred cover when a work has none.
    private var backdrop: some View {
        Color.clear
            .frame(height: 280)
            .overlay {
                if file.meta?.backdrop != nil {
                    RemoteImage(url: api.backdropURL(file))
                } else if file.thumb == true {
                    RemoteImage(url: api.thumbURL(file)).blur(radius: 24).scaleEffect(1.2)
                }
            }
            .clipped()
            .overlay {
                LinearGradient(stops: [
                    .init(color: .black.opacity(0.35), location: 0), // keeps the navigation legible
                    .init(color: .clear, location: 0.35),
                    .init(color: Color(.systemBackground), location: 1),
                ], startPoint: .top, endPoint: .bottom)
            }
    }

    /// Plays a file, or the first playable file of a movie folder. Files that
    /// cannot play open in the system preview.
    @ViewBuilder
    private var action: some View {
        if let target = playable {
            ActionButton(title: "播放", systemImage: "play.fill") { playing = target }
        } else if !file.dir {
            ActionButton(title: file.kind == "book" ? "阅读" : "查看", systemImage: file.kind == "book" ? "book.fill" : "eye.fill", busy: opening) { open() }
        }
    }

    private var playable: FileItem? {
        if !file.dir { return file.play == nil ? nil : file }
        guard file.meta?.kind == "movie" else { return nil }
        return pager.items.first { $0.play != nil }
    }

    private var contents: some View {
        LazyVStack(alignment: .leading, spacing: 0) {
            PagerStatus(pager: pager).frame(maxWidth: .infinity)
            ForEach(pager.items) { child in
                FileRow(file: child).task { await pager.loadMore(after: child) }
                Divider().padding(.leading, 16)
            }
        }
    }

    private var details: some View {
        VStack(alignment: .leading, spacing: 8) {
            ForEach(file.infoRows, id: \.label) { row in
                HStack(alignment: .firstTextBaseline, spacing: 12) {
                    Text(row.label).foregroundStyle(.secondary).frame(width: 72, alignment: .leading)
                    Text(row.value).textSelection(.enabled)
                }
                .font(.subheadline)
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(14)
        .background(Color(.secondarySystemBackground), in: .rect(cornerRadius: 12))
        .padding(16)
    }

    private func open() {
        opening = true
        error = nil
        Task {
            do {
                preview = try await api.download(file)
            } catch {
                self.error = error.localizedDescription
            }
            opening = false
        }
    }
}

private struct ActionButton: View {
    let title: String
    let systemImage: String
    var busy = false
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(spacing: 8) {
                if busy {
                    ProgressView().tint(.white)
                } else {
                    Image(systemName: systemImage)
                }
                Text(title).fontWeight(.semibold)
            }
            .foregroundStyle(.white)
            .frame(maxWidth: .infinity, minHeight: 46)
            .background(.tint, in: .rect(cornerRadius: 10))
        }
        .buttonStyle(.plain)
        .disabled(busy)
    }
}

/// A media tab: identified works of one semantic kind.
struct LibraryTab: View {
    let title: String
    let kind: String
    @Environment(\.api) private var api
    @State private var pager = Pager()

    var body: some View {
        NavigationStack {
            ScrollView {
                LazyVGrid(columns: [GridItem(.adaptive(minimum: 105), spacing: 12, alignment: .top)], spacing: 16) {
                    ForEach(pager.items) { item in
                        NavigationLink(value: item) {
                            VStack(alignment: .leading, spacing: 4) {
                                Color.clear
                                    .aspectRatio(kind == "album" ? 1 : 2.0 / 3.0, contentMode: .fit)
                                    .overlay { RemoteImage(url: item.thumb == true ? api.thumbURL(item) : nil, icon: "film") }
                                    .clipShape(.rect(cornerRadius: 8))
                                Text(item.title).font(.caption).lineLimit(2).foregroundStyle(.primary)
                                Text(item.subtitle).font(.caption2).lineLimit(1).foregroundStyle(.secondary)
                            }
                        }
                        .buttonStyle(.plain)
                        .task { await pager.loadMore(after: item) }
                    }
                }
                .padding()
            }
            .overlay { PagerStatus(pager: pager) }
            .navigationTitle(title)
            .itemDestinations()
            .loads(pager) { [api, kind] offset, limit in
                try await api.library(kind: kind, offset: offset, limit: limit)
            }
        }
    }
}
