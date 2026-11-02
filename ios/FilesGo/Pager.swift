import Observation
import SwiftUI

/// Offset paging over any list endpoint. Reloading keeps what is on screen and
/// asks for as many rows, so returning to the app does not lose the position.
@Observable
final class Pager {
    private static let pageSize = 60
    private(set) var items: [FileItem] = []
    private(set) var more = false
    private(set) var loaded = false
    private(set) var error: String?
    private var loading = false
    private let fetch: (_ offset: Int, _ limit: Int) async throws -> Page

    init(fetch: @escaping (_ offset: Int, _ limit: Int) async throws -> Page) {
        self.fetch = fetch
    }

    func reload() async {
        await load(offset: 0, limit: min(max(items.count, Self.pageSize), 1000))
    }

    func loadMore(after item: FileItem) async {
        guard more, !loading, item.id == items.last?.id else { return }
        await load(offset: items.count, limit: Self.pageSize)
    }

    private func load(offset: Int, limit: Int) async {
        guard !loading else { return }
        loading = true
        defer { loading = false; loaded = true }
        do {
            let page = try await fetch(offset, limit)
            items = offset == 0 ? page.items : items + page.items
            more = page.more ?? false
            error = nil
        } catch is CancellationError {
        } catch let failure as URLError where failure.code == .cancelled {
        } catch {
            self.error = error.localizedDescription
        }
    }
}

extension View {
    /// Loads on appearance, on pull-to-refresh and on returning to the app.
    func loads(_ pager: Pager) -> some View {
        modifier(PagerLoading(pager: pager))
    }
}

private struct PagerLoading: ViewModifier {
    let pager: Pager
    @Environment(\.scenePhase) private var phase

    func body(content: Content) -> some View {
        content
            .overlay {
                if pager.items.isEmpty {
                    if let error = pager.error {
                        ContentUnavailableView {
                            Label("加载失败", systemImage: "exclamationmark.triangle")
                        } description: {
                            Text(error)
                        } actions: {
                            Button("重试") { Task { await pager.reload() } }
                        }
                    } else if !pager.loaded {
                        ProgressView()
                    } else {
                        ContentUnavailableView("没有内容", systemImage: "tray")
                    }
                }
            }
            .task { if !pager.loaded { await pager.reload() } }
            .refreshable { await pager.reload() }
            .onChange(of: phase) { _, phase in
                if phase == .active { Task { await pager.reload() } }
            }
    }
}
