import QuickLook
import SwiftUI

struct RemoteImage: View {
    let file: FileItem
    @Environment(\.api) private var api
    @State private var image: UIImage?

    var body: some View {
        ZStack {
            Rectangle().fill(.quaternary)
            if let image {
                Image(uiImage: image).resizable().scaledToFill()
            } else {
                Image(systemName: icon).foregroundStyle(.secondary)
            }
        }
        .clipped()
        .task(id: file.version) {
            guard file.thumb == true, let data = try? await api.data(api.thumbURL(file)) else { return }
            image = UIImage(data: data)
        }
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
                RemoteImage(file: file)
                    .frame(width: 44, height: file.isPoster ? 66 : 44)
                    .clipShape(.rect(cornerRadius: 6))
                VStack(alignment: .leading, spacing: 2) {
                    Text(file.title).lineLimit(2)
                    Text(file.subtitle).font(.caption).foregroundStyle(.secondary).lineLimit(1)
                }
            }
        }
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
    @State private var opening: FileItem?

    var body: some View {
        List {
            if query.isEmpty {
                ForEach(roots) { root in
                    Button {
                        open(root)
                    } label: {
                        HStack {
                            Label(root.name, systemImage: "externaldrive")
                            Spacer()
                            if root.state != "idle" {
                                Text(stateLabel(root.state)).font(.caption).foregroundStyle(.secondary)
                            }
                        }
                    }
                    .tint(.primary)
                }
            } else {
                ForEach(results) { FileRow(file: $0) }
            }
        }
        .navigationTitle("文件")
        .navigationDestination(item: $opening) { ItemView(file: $0) }
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

    private func open(_ root: RootStatus) {
        Task {
            do {
                opening = try await api.file(root.fileId)
            } catch {
                self.error = error.localizedDescription
            }
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

/// The one detail screen: a work's or file's header, its actions, and its
/// children when it is a directory.
struct ItemView: View {
    let file: FileItem
    @Environment(\.api) private var api
    @State private var pager: Pager?
    @State private var playing = false
    @State private var preview: URL?
    @State private var downloading = false
    @State private var error: String?

    var body: some View {
        List {
            if !file.dir || file.meta?.kind != nil {
                Section { header }
            }
            if let pager {
                ForEach(pager.items) { child in
                    FileRow(file: child).task { await pager.loadMore(after: child) }
                }
            }
        }
        .navigationTitle(file.title)
        .navigationBarTitleDisplayMode(.inline)
        .modifier(OptionalPagerLoading(pager: pager))
        .onAppear {
            guard file.dir, pager == nil else { return }
            let api = api, id = file.id
            pager = Pager { try await api.children(id, offset: $0, limit: $1) }
        }
        .fullScreenCover(isPresented: $playing) { PlayerView(file: file) }
        .quickLookPreview($preview)
    }

    private var header: some View {
        VStack(alignment: .leading, spacing: 12) {
            if file.thumb == true {
                RemoteImage(file: file)
                    .aspectRatio(file.isPoster ? 2.0 / 3.0 : 1, contentMode: .fit)
                    .frame(maxWidth: 200)
                    .clipShape(.rect(cornerRadius: 8))
                    .frame(maxWidth: .infinity)
            }
            Text(file.title).font(.title3.bold())
            Text(file.subtitle).font(.subheadline).foregroundStyle(.secondary)
            if let summary = file.meta?.summary, !summary.isEmpty {
                Text(summary).font(.subheadline)
            }
            if !file.dir {
                HStack {
                    if file.play != nil {
                        Button("播放", systemImage: "play.fill") { playing = true }
                            .buttonStyle(.borderedProminent)
                    }
                    Button("打开", systemImage: "arrow.down.circle") { open() }
                        .buttonStyle(.bordered)
                        .disabled(downloading)
                    if downloading { ProgressView() }
                }
            }
            if let error {
                Text(error).font(.caption).foregroundStyle(.red)
            }
        }
    }

    private func open() {
        downloading = true
        error = nil
        Task {
            do {
                preview = try await api.download(file)
            } catch {
                self.error = error.localizedDescription
            }
            downloading = false
        }
    }
}

private struct OptionalPagerLoading: ViewModifier {
    let pager: Pager?

    func body(content: Content) -> some View {
        if let pager {
            content.loads(pager)
        } else {
            content
        }
    }
}

/// A media tab: identified works of one semantic kind.
struct LibraryTab: View {
    let title: String
    let kind: String
    @Environment(\.api) private var api
    @State private var pager: Pager?

    var body: some View {
        NavigationStack {
            Group {
                if let pager {
                    LibraryGrid(pager: pager, tall: kind != "album").loads(pager)
                }
            }
            .navigationTitle(title)
            .itemDestinations()
        }
        .onAppear {
            guard pager == nil else { return }
            let api = api, kind = kind
            pager = Pager { try await api.library(kind: kind, offset: $0, limit: $1) }
        }
    }
}

private struct LibraryGrid: View {
    let pager: Pager
    let tall: Bool

    var body: some View {
        ScrollView {
            LazyVGrid(columns: [GridItem(.adaptive(minimum: 105), spacing: 12, alignment: .top)], spacing: 16) {
                ForEach(pager.items) { item in
                    NavigationLink(value: item) {
                        VStack(alignment: .leading, spacing: 4) {
                            Color.clear
                                .aspectRatio(tall ? 2.0 / 3.0 : 1, contentMode: .fit)
                                .overlay { RemoteImage(file: item) }
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
    }
}
