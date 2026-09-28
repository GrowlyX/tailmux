// swift-tools-version:5.10
import PackageDescription

let package = Package(
    name: "TailmuxBar",
    platforms: [.macOS(.v13)],
    targets: [
        .executableTarget(name: "TailmuxBar", path: "Sources/TailmuxBar")
    ]
)
