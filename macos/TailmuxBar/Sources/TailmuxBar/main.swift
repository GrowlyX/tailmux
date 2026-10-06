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

// Snapshot mode, for screenshots and for checking the UI in CI without a
// menu bar:
//   TailmuxBar --snapshot out.png [--dark] [--wait 1]            the menu bar panel
//   TailmuxBar --snapshot out.png --page settings [--dark]       a page of the main window
//   --scale 2 renders at 2x whatever the screen (the README banner uses it).
// Views are rendered in a real offscreen window, so native controls
// (text fields, toggles, lists) draw as they do on screen.
let args = CommandLine.arguments

func arg(_ name: String) -> String? {
    args.firstIndex(of: name).flatMap { $0 + 1 < args.count ? args[$0 + 1] : nil }
}

@MainActor
func renderWindow<V: View>(_ view: V, size: NSSize, dark: Bool, scale: CGFloat? = nil) -> Data? {
    let w = NSWindow(contentRect: NSRect(origin: .zero, size: size), styleMask: [.borderless], backing: .buffered, defer: false)
    w.appearance = NSAppearance(named: dark ? .darkAqua : .aqua)
    let host = NSHostingView(rootView: view.frame(width: size.width, height: size.height))
    host.frame = NSRect(origin: .zero, size: size)
    w.contentView = host
    w.orderFront(nil)
    host.layoutSubtreeIfNeeded()
    RunLoop.current.run(until: Date().addingTimeInterval(0.8)) // let lists and charts lay out
    var rep = host.bitmapImageRepForCachingDisplay(in: host.bounds)
    if let scale {
        rep = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: Int(size.width * scale), pixelsHigh: Int(size.height * scale),
                               bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true, isPlanar: false,
                               colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0)
        rep?.size = size
    }
    guard let rep else { return nil }
    host.cacheDisplay(in: host.bounds, to: rep)
    w.orderOut(nil)
    return rep.representation(using: .png, properties: [:])
}

if let outPath = arg("--snapshot") {
    let out = URL(fileURLWithPath: outPath)
    let dark = args.contains("--dark")
    let wait = arg("--wait").flatMap(Double.init) ?? 1
    let scale = arg("--scale").flatMap(Double.init).map { CGFloat($0) }
    NSApplication.shared.setActivationPolicy(.accessory)
    _ = Task { @MainActor in
        let store = Store()
        store.start()
        try? await Task.sleep(nanoseconds: UInt64(wait * 1e9))
        await store.refresh()
        var png: Data?
        if let pageName = arg("--page"), let page = Page(rawValue: pageName) {
            let m = Manager(store: store)
            m.page = page
            await m.load(resetForm: true)
            png = renderWindow(MainWindow(store: store, manager: m).background(Color(nsColor: .windowBackgroundColor)),
                               size: NSSize(width: 980, height: 640), dark: dark, scale: scale)
        } else {
            let panel = PanelView(store: store, snapshot: true).background(Color(nsColor: .windowBackgroundColor))
            let size = NSHostingView(rootView: panel).fittingSize
            png = renderWindow(panel, size: size, dark: dark, scale: scale)
        }
        guard let png else {
            FileHandle.standardError.write("render failed\n".data(using: .utf8)!)
            exit(1)
        }
        try png.write(to: out)
        print("wrote \(out.path) (\(store.tailnets.count) tailnets, offline: \(store.offline ?? "no"))")
        exit(0)
    }
    NSApplication.shared.run()
} else {
    TailmuxBarApp.main()
}
