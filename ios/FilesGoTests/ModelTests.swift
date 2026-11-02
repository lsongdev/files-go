import XCTest
@testable import FilesGo

@MainActor
final class ModelTests: XCTestCase {
    private func item(_ json: String) throws -> FileItem {
        try JSONDecoder().decode(FileItem.self, from: Data(json.utf8))
    }

    func testDecodesServerViewAndFormatsLikeTheWeb() throws {
        let movie = try item(#"""
        {"id":7,"root":"movies","name":"Dune (2021)","dir":true,"size":3,"mtime":1,"kind":"folder","version":4,"thumb":true,
         "meta":{"kind":"movie","title":"沙丘","year":2021,"rating":8.04,"genres":["科幻","冒险","剧情"],"duration":9300,"poster":"file:9","artworkVersion":"2:","info":{"Code":"x"},"subtitles":[{"index":0,"codec":"srt"}],"series":{"title":"x"},"match":{"provider":"tmdb","type":"movie","id":"1"}}}
        """#)
        XCTAssertEqual(movie.title, "沙丘")
        XCTAssertEqual(movie.subtitle, "2021 · 科幻 / 冒险 · ★ 8.0 · 2:35:00")
        XCTAssertEqual(movie.coverAspect, 2.0 / 3.0)
        XCTAssertTrue(movie.hasHero)
        XCTAssertEqual(movie.kindLabel, "电影")
        XCTAssertEqual(movie.infoRows.first { $0.label == "番号" }?.value, "x")
        XCTAssertEqual(movie.infoRows.first { $0.label == "类型标签" }?.value, "科幻 / 冒险 / 剧情")
        XCTAssertNil(movie.infoRows.first { $0.label == "大小" })

        let episode = try item(#"{"id":8,"root":"tv","name":"e.mkv","dir":false,"size":1,"version":1,"kind":"video","play":"stream","meta":{"kind":"episode","show":"Foo","season":2,"episode":3}}"#)
        XCTAssertEqual(episode.title, "Foo · S02E03")

        let folder = try item(#"{"id":9,"root":"x","name":"Plain","dir":true,"size":12,"version":1}"#)
        XCTAssertEqual(folder.title, "Plain")
        XCTAssertEqual(folder.subtitle, "12 项")
        XCTAssertFalse(folder.hasHero)
    }

    func testServerAddressAcceptsBareHostAndPort() {
        XCTAssertEqual(APIClient.serverURL(from: " 192.168.8.100:8088/ ")?.absoluteString, "http://192.168.8.100:8088")
        XCTAssertEqual(APIClient.serverURL(from: "https://nas.example/files/")?.absoluteString, "https://nas.example/files")
        XCTAssertNil(APIClient.serverURL(from: "ftp://nas"))
        XCTAssertNil(APIClient.serverURL(from: ""))
    }

    func testRequestsAndURLs() throws {
        let api = APIClient(baseURL: URL(string: "http://nas:8088")!, username: "me", password: "pw")
        XCTAssertEqual(api.request(api.url("/api/roots")).value(forHTTPHeaderField: "Authorization"), "Basic bWU6cHc=")
        XCTAssertNil(APIClient(baseURL: api.baseURL).authorization)
        let file = try item(#"{"id":7,"root":"m","name":"a","dir":true,"size":0,"version":4,"meta":{"poster":"file:9","artworkVersion":"2:"}}"#)
        XCTAssertEqual(api.thumbURL(file).absoluteString, "http://nas:8088/api/files/7/thumb?v=4-2:-file:9")
    }
}
