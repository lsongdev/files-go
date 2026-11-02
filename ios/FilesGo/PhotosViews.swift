import ImageIO
import SwiftUI
import UIKit

/// Photos tab: a square grid, newest first, opening a full-screen viewer. Only
/// the photos root is listed, since any image elsewhere (document scans, album
/// art) is also identified as a photo.
struct PhotosTab: View {
    @Environment(\.api) private var api
    @State private var pager = Pager()
    @State private var viewing: FileItem?

    private let columns = Array(repeating: GridItem(.flexible(), spacing: 2), count: 3)

    var body: some View {
        NavigationStack {
            ScrollView {
                LazyVGrid(columns: columns, spacing: 2) {
                    ForEach(pager.items) { photo in
                        Button {
                            viewing = photo
                        } label: {
                            Color.clear
                                .aspectRatio(1, contentMode: .fit)
                                .overlay { RemoteImage(url: photo.thumb == true ? api.thumbURL(photo) : nil, releasesOnDisappear: true) }
                                .clipped()
                                .contentShape(.rect)
                        }
                        .buttonStyle(.plain)
                        .accessibilityLabel(photo.title)
                        .task { await pager.loadMore(after: photo) }
                    }
                }
            }
            .overlay { PagerStatus(pager: pager) }
            .navigationTitle("Photos")
            .loads(pager) { [api] offset, limit in
                let root = RootStatus.photoRoot(in: try await api.roots())
                return try await api.library(kind: "photo", root: root?.id, sort: "recent", offset: offset, limit: limit)
            }
            .fullScreenCover(item: $viewing) { PhotoViewer(pager: pager, start: $0) }
        }
    }
}

/// Pages through the loaded photos, fetching more on reaching the end.
private struct PhotoViewer: View {
    let pager: Pager
    @State private var current: Int64?
    @State private var showInfo = false
    @Environment(\.dismiss) private var dismiss

    init(pager: Pager, start: FileItem) {
        self.pager = pager
        _current = State(initialValue: start.id)
    }

    private var photo: FileItem? { pager.items.first { $0.id == current } }

    var body: some View {
        ScrollView(.horizontal) {
            LazyHStack(spacing: 0) {
                ForEach(pager.items) { item in
                    PhotoPage(file: item).containerRelativeFrame(.horizontal)
                }
            }
            .scrollTargetLayout()
        }
        .scrollTargetBehavior(.paging)
        .scrollPosition(id: $current)
        .scrollIndicators(.hidden)
        .background(.black)
        .ignoresSafeArea()
        .overlay(alignment: .top) { controls }
        .onChange(of: current) {
            guard let current, let index = pager.items.firstIndex(where: { $0.id == current }),
                  index >= pager.items.count - 5, let last = pager.items.last else { return }
            Task { await pager.loadMore(after: last) }
        }
        .sheet(isPresented: $showInfo) {
            if let photo { PhotoInfo(file: photo) }
        }
    }

    private var controls: some View {
        HStack {
            Button("Close", systemImage: "xmark") { dismiss() }
            Spacer()
            VStack(spacing: 1) {
                if let photo {
                    Text(photo.title).font(.subheadline.weight(.semibold)).lineLimit(1)
                    if let date = photo.modified {
                        Text(date, format: .dateTime.year().month().day()).font(.caption2)
                    }
                }
            }
            .foregroundStyle(.white)
            .shadow(radius: 3)
            Spacer()
            Button("Info", systemImage: "info.circle") { showInfo = true }
        }
        .labelStyle(.iconOnly)
        .buttonStyle(.bordered)
        .tint(.white)
        .padding(.horizontal)
    }
}

/// Shows the thumbnail at once, then the full image.
private struct PhotoPage: View {
    let file: FileItem
    @Environment(\.api) private var api
    @State private var image: UIImage?

    var body: some View {
        ZoomableImage(image: image)
            .overlay {
                if image == nil && file.thumb != true {
                    Image(systemName: "photo").font(.largeTitle).foregroundStyle(.secondary)
                }
            }
            .task(id: file.id) {
                if file.thumb == true, let data = try? await api.data(api.thumbURL(file)) {
                    image = UIImage(data: data)
                }
                guard let data = try? await api.data(api.rawURL(file)),
                      let full = await Task.detached(priority: .userInitiated, operation: { downsampled(data) }).value,
                      !Task.isCancelled else { return }
                image = full
            }
    }
}

/// Decodes at most `maxPixels` on the long edge, upright, so a large original
/// costs a bounded amount of memory while still allowing zoom.
nonisolated func downsampled(_ data: Data, maxPixels: Int = 4096) -> UIImage? {
    guard let source = CGImageSourceCreateWithData(data as CFData, nil),
          let image = CGImageSourceCreateThumbnailAtIndex(source, 0, [
              kCGImageSourceCreateThumbnailFromImageAlways: true,
              kCGImageSourceCreateThumbnailWithTransform: true,
              kCGImageSourceThumbnailMaxPixelSize: maxPixels,
              kCGImageSourceShouldCacheImmediately: true,
          ] as CFDictionary) else { return nil }
    return UIImage(cgImage: image)
}

private struct PhotoInfo: View {
    let file: FileItem
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            List {
                LabeledContent("Name", value: file.name)
                if let date = file.modified {
                    LabeledContent("Modified") { Text(date, format: .dateTime) }
                }
                ForEach(file.infoRows, id: \.label) { LabeledContent($0.label, value: $0.value) }
            }
            .navigationTitle("Info")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .confirmationAction) { Button("Done") { dismiss() } }
            }
        }
        .presentationDetents([.medium, .large])
    }
}

private struct ZoomableImage: UIViewRepresentable {
    let image: UIImage?

    func makeUIView(context: Context) -> ZoomingImageView { ZoomingImageView() }
    func updateUIView(_ view: ZoomingImageView, context: Context) { view.image = image }
}

/// At normal size it does not scroll, so swipes reach the pager around it.
final class ZoomingImageView: UIScrollView, UIScrollViewDelegate {
    private let imageView = UIImageView()
    private var laidOutSize = CGSize.zero

    var image: UIImage? {
        get { imageView.image }
        set {
            guard newValue !== imageView.image else { return }
            let changedShape = newValue?.size.aspect != imageView.image?.size.aspect
            imageView.image = newValue
            if changedShape { laidOutSize = .zero; setNeedsLayout() }
        }
    }

    init() {
        super.init(frame: .zero)
        delegate = self
        minimumZoomScale = 1
        maximumZoomScale = 5
        showsHorizontalScrollIndicator = false
        showsVerticalScrollIndicator = false
        contentInsetAdjustmentBehavior = .never
        imageView.contentMode = .scaleAspectFit
        addSubview(imageView)
        let doubleTap = UITapGestureRecognizer(target: self, action: #selector(toggleZoom))
        doubleTap.numberOfTapsRequired = 2
        addGestureRecognizer(doubleTap)
    }

    required init?(coder: NSCoder) { fatalError("init(coder:) is not supported") }

    override func layoutSubviews() {
        super.layoutSubviews()
        if bounds.size != laidOutSize, let size = imageView.image?.size, size.width > 0, size.height > 0, bounds.width > 0 {
            laidOutSize = bounds.size
            zoomScale = 1
            let scale = min(bounds.width / size.width, bounds.height / size.height)
            imageView.frame = CGRect(x: 0, y: 0, width: size.width * scale, height: size.height * scale)
            contentSize = imageView.frame.size
        }
        centerImage()
    }

    func viewForZooming(in scrollView: UIScrollView) -> UIView? { imageView }
    func scrollViewDidZoom(_ scrollView: UIScrollView) { centerImage() }

    private func centerImage() {
        contentInset = UIEdgeInsets(
            top: max((bounds.height - contentSize.height) / 2, 0), left: max((bounds.width - contentSize.width) / 2, 0),
            bottom: 0, right: 0)
    }

    @objc private func toggleZoom(_ gesture: UITapGestureRecognizer) {
        if zoomScale > 1 {
            setZoomScale(1, animated: true)
        } else {
            let point = gesture.location(in: imageView)
            let size = CGSize(width: bounds.width / 2.5, height: bounds.height / 2.5)
            zoom(to: CGRect(x: point.x - size.width / 2, y: point.y - size.height / 2, width: size.width, height: size.height), animated: true)
        }
    }
}

private extension CGSize {
    var aspect: CGFloat { height > 0 ? width / height : 0 }
}
