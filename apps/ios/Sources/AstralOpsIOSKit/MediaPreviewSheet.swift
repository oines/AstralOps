import SwiftUI
import UIKit

struct MediaPreviewItem: Identifiable {
    var media: MediaReadResult
    var id: String {
        "\(media.sessionID)-\(media.eventSeq)-\(media.mediaID)"
    }
}

struct MediaPreviewSheet: View {
    let media: MediaReadResult

    private var data: Data? {
        Data(base64Encoded: media.contentBase64)
    }

    var body: some View {
        NavigationStack {
            VStack(spacing: 16) {
                if let data, (media.mimeType ?? "").hasPrefix("image/"), let image = UIImage(data: data) {
                    Image(uiImage: image)
                        .resizable()
                        .scaledToFit()
                        .frame(maxWidth: .infinity, maxHeight: .infinity)
                } else if let data, let text = String(data: data, encoding: .utf8) {
                    ScrollView {
                        Text(text)
                            .font(.system(.body, design: .monospaced))
                            .textSelection(.enabled)
                            .frame(maxWidth: .infinity, alignment: .leading)
                            .padding()
                    }
                } else {
                    ContentUnavailableView("Preview unavailable", systemImage: "doc", description: Text(media.mimeType ?? "Unknown media type"))
                }
            }
            .padding()
            .navigationTitle(media.name)
            .navigationBarTitleDisplayMode(.inline)
        }
    }
}
