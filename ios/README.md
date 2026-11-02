# FilesGo for iOS

SwiftUI client for a files-go server. Design: [docs/mobile-app.md](../docs/mobile-app.md).

```sh
brew install xcodegen
cd ios
xcodegen generate      # FilesGo.xcodeproj is generated from project.yml, not committed
open FilesGo.xcodeproj
```

To run on a device, select your team under Signing & Capabilities (or set
`DEVELOPMENT_TEAM` in `project.yml`). Requires iOS 18, and a server with
`/api/library` for the media tabs.

Unit tests:

```sh
xcodebuild test -project FilesGo.xcodeproj -scheme FilesGo \
  -destination 'platform=iOS Simulator,name=iPhone 16' CODE_SIGNING_ALLOWED=NO
```
