import AVFoundation
import AVKit
import Combine
import SwiftUI

/// Plays audio and video through the server's HLS timeline. When the device
/// cannot play the stream as is (VP9, AV1 ...), it retries once transcoded.
@Observable
final class PlaybackController {
    private(set) var player: AVPlayer?
    private(set) var error: String?
    private var current: URL?
    private var observer: AnyCancellable?

    func start(_ file: FileItem, api: APIClient, transcode: Bool = false) async {
        try? AVAudioSession.sharedInstance().setCategory(.playback)
        try? AVAudioSession.sharedInstance().setActive(true)
        do {
            let url = try await api.play(file.id, transcode: transcode)
            guard !Task.isCancelled else { await api.release(playback: url); return }
            current = url
            let item = AVPlayerItem(url: url)
            player = AVPlayer(playerItem: item)
            player?.play()
            // A sink, not publisher.values: the async sequence can miss the change to failed.
            observer = item.publisher(for: \.status)
                .receive(on: DispatchQueue.main)
                .filter { $0 == .failed }
                .first()
                .sink { [weak self] _ in
                    Task { await self?.failed(item, file: file, api: api, transcode: transcode) }
                }
        } catch {
            self.error = error.localizedDescription
        }
    }

    private func failed(_ item: AVPlayerItem, file: FileItem, api: APIClient, transcode: Bool) async {
        await release(api)
        guard observer != nil else { return } // closed while failing
        if transcode {
            error = item.error?.localizedDescription ?? "无法播放"
        } else {
            await start(file, api: api, transcode: true)
        }
    }

    func stop(_ api: APIClient) {
        observer = nil
        Task { await release(api) }
    }

    private func release(_ api: APIClient) async {
        player?.pause()
        player = nil
        if let current {
            self.current = nil
            await api.release(playback: current)
        }
    }
}

struct PlayerView: View {
    let file: FileItem
    @Environment(\.api) private var api
    @Environment(\.dismiss) private var dismiss
    @State private var controller = PlaybackController()

    var body: some View {
        ZStack(alignment: .topLeading) {
            Color.black.ignoresSafeArea()
            if let player = controller.player {
                VideoPlayer(player: player).ignoresSafeArea()
            } else if let error = controller.error {
                ContentUnavailableView("无法播放", systemImage: "exclamationmark.triangle", description: Text(error))
                    .foregroundStyle(.white)
            } else {
                ProgressView().tint(.white).frame(maxWidth: .infinity, maxHeight: .infinity)
            }
            Button("关闭", systemImage: "xmark") { dismiss() }
                .labelStyle(.iconOnly)
                .buttonStyle(.bordered)
                .tint(.white)
                .padding()
        }
        .task { await controller.start(file, api: api) }
        .onDisappear { controller.stop(api) }
    }
}
