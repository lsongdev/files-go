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
            Tab("文件", systemImage: "folder") { NavigationStack { RootsView().itemDestinations() } }
            Tab("电影", systemImage: "film") { LibraryTab(title: "电影", kind: "movie") }
            Tab("电视剧", systemImage: "tv") { LibraryTab(title: "电视剧", kind: "show") }
            Tab("音乐", systemImage: "music.note") { LibraryTab(title: "音乐", kind: "album") }
            Tab("图书", systemImage: "book") { LibraryTab(title: "图书", kind: "book") }
        }
    }
}

extension View {
    func itemDestinations() -> some View {
        navigationDestination(for: FileItem.self) { ItemView(file: $0) }
    }
}
