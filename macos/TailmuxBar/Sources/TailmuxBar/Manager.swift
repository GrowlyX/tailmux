import AppKit
import Foundation
import ServiceManagement
import SwiftUI

enum Page: String, CaseIterable, Identifiable {
    case overview, tailnets, devices, exitNode = "exit-node", settings, logs
    var id: String { rawValue }

    var title: String { self == .exitNode ? "Exit node" : rawValue.capitalized }

    var icon: String {
        switch self {
        case .overview: return "gauge.with.dots.needle.33percent"
        case .tailnets: return "circle.grid.3x3.fill"
        case .devices: return "desktopcomputer"
        case .exitNode: return "arrow.up.forward.circle"
        case .settings: return "gearshape"
        case .logs: return "text.alignleft"
        }
    }
}

/// State for the main window. The live status and throughput come from
/// the shared Store; this adds what only the window needs.
@MainActor
final class Manager: ObservableObject {
    @Published var page: Page = .overview
    @Published var peers: [PeerInfo] = []
    @Published var config: ConfigView?
    @Published var logs: [String] = []
    @Published var query = ""
    @Published var busy = false
    @Published var message: String?
    @Published var error: String?
    @Published var restartPending = false

    // Exit node page.
    @Published var exitQuery = ""
    // Countries the user opened or closed; the one with the selected
    // node starts open.
    @Published var toggled: Set<String> = []

    // Add-tailnet sheet.
    @Published var showAdd = false
    @Published var newName = ""
    @Published var newControl = ""
    @Published var newKey = ""
    @Published var addedLoginURL: String?

    // Settings form.
    @Published var hostname = ""
    @Published var tun = false
    @Published var autoUpdate = true
    @Published var loginItem = SMAppService.mainApp.status == .enabled

    let store: Store
    private var timer: Timer?

    init(store: Store) {
        self.store = store
    }

    var api: API { store.api }

    func start() {
        Task { await load(resetForm: true) }
        timer?.invalidate()
        timer = Timer.scheduledTimer(withTimeInterval: 2, repeats: true) { [weak self] _ in
            Task { @MainActor in await self?.load(resetForm: false) }
        }
    }

    func stop() {
        timer?.invalidate()
        timer = nil
    }

    func load(resetForm: Bool) async {
        do {
            async let p = api.get("peers", as: [PeerInfo].self)
            async let c = api.get("config", as: ConfigView.self)
            async let l = api.getLogs(300)
            let (peers, config, logs) = try await (p, c, l)
            self.peers = peers
            self.config = config
            self.logs = logs
            if resetForm { fillForm(from: config) }
            if page == .exitNode { await store.refreshExitNodes() }
        } catch {
            // The Store reports the daemon being down; nothing to add.
        }
    }

    private func fillForm(from c: ConfigView) {
        hostname = c.config.hostname ?? ""
        tun = c.config.tun?.enabled ?? false
        autoUpdate = c.config.updates?.auto ?? true
    }

    var filteredPeers: [PeerInfo] {
        let q = query.trimmingCharacters(in: .whitespaces).lowercased()
        let sorted = peers.sorted { ($0.online ? 0 : 1, $0.alias) < ($1.online ? 0 : 1, $1.alias) }
        guard !q.isEmpty else { return sorted }
        return sorted.filter {
            $0.alias.lowercased().contains(q) || ($0.ips ?? []).contains { $0.contains(q) }
        }
    }

    // MARK: actions

    private func run(_ done: String?, _ body: @escaping () async throws -> Void) {
        busy = true
        error = nil
        Task {
            do {
                try await body()
                if let done { message = done }
            } catch {
                self.error = error.localizedDescription
            }
            busy = false
            await store.refresh()
            await load(resetForm: false)
        }
    }

    func addTailnet() {
        let name = newName.trimmingCharacters(in: .whitespaces).lowercased()
        var body: [String: Any] = ["name": name]
        if !newControl.isEmpty { body["control_url"] = newControl }
        if !newKey.isEmpty { body["auth_key"] = newKey }
        run(nil) {
            let data = try await self.api.send("POST", "tailnets", json: body)
            let st = try self.api.decode(data, as: TailnetStatus.self)
            if let url = st.authUrl, !url.isEmpty {
                self.addedLoginURL = url
            } else {
                self.showAdd = false
                self.message = "Joined \(name)."
            }
            self.newName = ""
            self.newControl = ""
            self.newKey = ""
        }
    }

    func remove(_ name: String) {
        run("Removed \(name). Its login is kept if you add it back.") {
            try await self.api.send("DELETE", "tailnets/\(name)")
        }
    }

    func setExitNode(_ node: ExitNodeInfo?) {
        let done = node.map { "Exit node: \($0.name). Applied now." } ?? "Exit node off."
        run(done) {
            try await self.api.setExitNode(tailnet: node?.tailnet, node: node?.spec)
            await self.store.refreshExitNodes()
        }
    }

    func saveSettings() {
        run("Saved. Restart the service to apply.") {
            try await self.api.send("PUT", "settings", json: [
                "tun": self.tun, "auto_update": self.autoUpdate, "hostname": self.hostname,
            ])
            self.restartPending = true
        }
    }

    func restartService() {
        run("Restarting the service…") {
            try await self.api.send("POST", "restart")
            self.restartPending = false
        }
    }

    func repair() {
        run("Routes and DNS re-applied, DNS cache flushed.") {
            try await self.api.send("POST", "repair")
        }
    }

    func checkForUpdates() {
        run("Checking for updates…") {
            try await self.api.send("POST", "update")
        }
    }

    func setLoginItem(_ on: Bool) {
        do {
            if on { try SMAppService.mainApp.register() } else { try SMAppService.mainApp.unregister() }
        } catch {
            self.error = "Open at login: \(error.localizedDescription)"
        }
        loginItem = SMAppService.mainApp.status == .enabled
    }

    func openLogin(_ url: String) {
        if let u = URL(string: url) { NSWorkspace.shared.open(u) }
    }

    func copy(_ s: String) {
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(s, forType: .string)
        message = "Copied \(s)"
    }
}

/// Opens the one main window, creating it on first use. Done by hand
/// (not a SwiftUI Window scene) so it never pops up at launch or login.
@MainActor
final class MainWindowController {
    static let shared = MainWindowController()
    private var window: NSWindow?
    private var manager: Manager?

    func show(store: Store, page: Page? = nil) {
        if window == nil {
            let m = Manager(store: store)
            manager = m
            let w = NSWindow(
                contentRect: NSRect(x: 0, y: 0, width: 980, height: 640),
                styleMask: [.titled, .closable, .miniaturizable, .resizable, .fullSizeContentView],
                backing: .buffered, defer: false)
            w.title = "tailmux"
            w.titlebarAppearsTransparent = true
            w.isReleasedWhenClosed = false
            w.contentMinSize = NSSize(width: 820, height: 520)
            w.contentViewController = NSHostingController(rootView: MainWindow(store: store, manager: m))
            w.center()
            NotificationCenter.default.addObserver(forName: NSWindow.willCloseNotification, object: w, queue: .main) { _ in
                Task { @MainActor in
                    self.manager?.stop()
                    // Back to a menu-bar-only app once the window is gone.
                    NSApp.setActivationPolicy(.accessory)
                }
            }
            window = w
        }
        if let page { manager?.page = page }
        manager?.start()
        NSApp.setActivationPolicy(.regular)
        NSApp.activate(ignoringOtherApps: true)
        window?.makeKeyAndOrderFront(nil)
    }
}
