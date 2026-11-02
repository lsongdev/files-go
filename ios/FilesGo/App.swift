import SwiftUI

@main
struct FilesGoApp: App {
    @State private var session = Session()

    var body: some Scene {
        WindowGroup {
            Group {
                if let client = session.client {
                    MainTabs().environment(\.api, client).id(client.baseURL)
                } else {
                    ConnectView()
                }
            }
            .environment(session)
        }
    }
}

struct MainTabs: View {
    var body: some View {
        TabView {
            Tab("Movies", systemImage: "film") { LibraryTab(title: "Movies", kind: "movie") }
            Tab("TV Shows", systemImage: "tv") { LibraryTab(title: "TV Shows", kind: "show") }
            Tab("Music", systemImage: "music.note") { LibraryTab(title: "Music", kind: "album") }
            Tab("Books", systemImage: "book") { LibraryTab(title: "Books", kind: "book") }
            Tab("More", systemImage: "ellipsis") { MoreTab() }
        }
    }
}

/// Holds what the tab bar has no room for. It is an ordinary tab rather than
/// the system's More, whose navigation bar would stack on top of a NavigationStack.
struct MoreTab: View {
    /// Links are values throughout: mixing them with destination-view links
    /// in one stack shows the pushed screens in the wrong order.
    private enum Section: Hashable { case files, photos }

    var body: some View {
        NavigationStack {
            List {
                NavigationLink(value: Section.files) {
                    Label("Files", systemImage: "folder")
                }
                NavigationLink(value: Section.photos) {
                    Label("Photos", systemImage: "photo.on.rectangle")
                }
            }
            .navigationTitle("More")
            .navigationDestination(for: Section.self) { section in
                switch section {
                case .files: RootsView()
                case .photos: PhotosView()
                }
            }
            .itemDestinations()
        }
    }
}

extension View {
    func itemDestinations() -> some View {
        navigationDestination(for: FileItem.self) { ItemView(file: $0) }
    }
}
