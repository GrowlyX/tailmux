import AppKit
import SwiftUI

struct TailmuxBarApp: App {
    @StateObject private var store = Store()

    var body: some Scene {
        MenuBarExtra {
            PanelView(store: store)
        } label: {
            Image(nsImage: DotsIcon.image(lit: store.runningCount, reachable: store.offline == nil))
                .onAppear { store.start() }
        }
        .menuBarExtraStyle(.window)
    }
}

// `TailmuxBar --snapshot out.png` renders the panel against the running
// daemon and exits: used for screenshots and to check the UI in CI
// without a menu bar.
let args = CommandLine.arguments
if let i = args.firstIndex(of: "--snapshot"), i + 1 < args.count {
    let out = URL(fileURLWithPath: args[i + 1])
    let dark = args.contains("--dark")
    let wait = args.firstIndex(of: "--wait").flatMap { Double(args[$0 + 1]) } ?? 1
    NSApplication.shared.setActivationPolicy(.prohibited)
    _ = Task { @MainActor in
        let store = Store()
        store.start()
        try? await Task.sleep(nanoseconds: UInt64(wait * 1e9))
        await store.refresh()
        let view = PanelView(store: store, snapshot: true)
            .background(Color(nsColor: .windowBackgroundColor))
            .environment(\.colorScheme, dark ? .dark : .light)
        let r = ImageRenderer(content: view)
        r.scale = 2
        let appearance = NSAppearance(named: dark ? .darkAqua : .aqua)!
        var png: Data?
        appearance.performAsCurrentDrawingAppearance {
            if let cg = r.cgImage {
                png = NSBitmapImageRep(cgImage: cg).representation(using: .png, properties: [:])
            }
        }
        guard let png else {
            FileHandle.standardError.write("render failed\n".data(using: .utf8)!)
            exit(1)
        }
        try png.write(to: out)
        // The menu bar icon too, at a few fill levels.
        for n in [0, 3, 5] {
            let img = DotsIcon.image(lit: n, reachable: true, size: 64)
            if let tiff = img.tiffRepresentation, let rep = NSBitmapImageRep(data: tiff),
               let data = rep.representation(using: .png, properties: [:]) {
                try data.write(to: out.deletingPathExtension().appendingPathExtension("icon\(n).png"))
            }
        }
        print("wrote \(out.path) (\(store.tailnets.count) tailnets, offline: \(store.offline ?? "no"))")
        exit(0)
    }
    NSApplication.shared.run()
} else {
    TailmuxBarApp.main()
}
