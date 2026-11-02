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
            Tab("Files", systemImage: "folder") { NavigationStack { RootsView().itemDestinations() } }
            Tab("Movies", systemImage: "film") { LibraryTab(title: "Movies", kind: "movie") }
            Tab("TV Shows", systemImage: "tv") { LibraryTab(title: "TV Shows", kind: "show") }
            Tab("Photos", systemImage: "photo.on.rectangle") { PhotosTab() }
            // iPhone shows at most five tabs, so these two go under More.
            Tab("Music", systemImage: "music.note") { LibraryTab(title: "Music", kind: "album") }
            Tab("Books", systemImage: "book") { LibraryTab(title: "Books", kind: "book") }
        }
    }
}

extension View {
    func itemDestinations() -> some View {
        navigationDestination(for: FileItem.self) { ItemView(file: $0) }
    }
}
