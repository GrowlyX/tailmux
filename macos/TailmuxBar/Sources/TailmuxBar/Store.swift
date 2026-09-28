import AppKit
import Foundation
import SwiftUI

/// Polls the daemon once a second and holds what the menu shows.
@MainActor
final class Store: ObservableObject {
    @Published var status: StatusResponse?
    @Published var stats: [String: TailnetStats] = [:]
    @Published var offline: String?
    @Published var pending: Set<String> = []

    let api: API
    private var timer: Timer?

    init(api: API = .discover()) {
        self.api = api
    }

    func start() {
        guard timer == nil else { return }
        Task { await refresh() }
        timer = Timer.scheduledTimer(withTimeInterval: 1, repeats: true) { [weak self] _ in
            Task { @MainActor in await self?.refresh() }
        }
    }

    func refresh() async {
        do {
            async let st = api.get("status", as: StatusResponse.self)
            async let sx = api.get("stats", as: StatsResponse.self)
            let (s, x) = try await (st, sx)
            status = s
            stats = Dictionary(uniqueKeysWithValues: x.tailnets.map { ($0.name, $0) })
            offline = nil
            relaunchIfUpgraded(daemonVersion: s.version)
        } catch {
            status = nil
            stats = [:]
            offline = error.localizedDescription
        }
    }

    func startUpdate() {
        Task {
            do { try await api.startUpdate() } catch { offline = error.localizedDescription }
            await refresh()
        }
    }

    /// Homebrew upgrades the app together with the daemon. Once the
    /// daemon is running a version this app isn't, relaunch from the
    /// installed bundle that matches it.
    private func relaunchIfUpgraded(daemonVersion: String?) {
        guard let dv = daemonVersion, dv != "dev", !relaunching,
              let mine = Bundle.main.infoDictionary?["CFBundleShortVersionString"] as? String,
              mine != dv, mine != "ci", !mine.hasPrefix("0.0.0")
        else { return }
        let candidates = ["/opt/homebrew/opt/tailmux/TailmuxBar.app", "/usr/local/opt/tailmux/TailmuxBar.app", "/Applications/TailmuxBar.app"]
        for path in candidates {
            guard let info = NSDictionary(contentsOfFile: path + "/Contents/Info.plist"),
                  info["CFBundleShortVersionString"] as? String == dv
            else { continue }
            relaunching = true
            let p = Process()
            p.executableURL = URL(fileURLWithPath: "/usr/bin/open")
            p.arguments = ["-n", path]
            try? p.run()
            DispatchQueue.main.asyncAfter(deadline: .now() + 1) { NSApp.terminate(nil) }
            return
        }
    }

    private var relaunching = false

    func setEnabled(_ name: String, _ on: Bool) {
        pending.insert(name)
        // Show the switch flip right away; the next poll confirms it.
        if let i = status?.tailnets.firstIndex(where: { $0.name == name }) {
            status?.tailnets[i].enabled = on
        }
        Task {
            do { try await api.setEnabled(name, on) } catch { offline = error.localizedDescription }
            pending.remove(name)
            await refresh()
        }
    }

    // MARK: derived

    var tailnets: [TailnetStatus] { status?.tailnets ?? [] }
    var runningCount: Int { tailnets.filter(\.running).count }

    /// Latest bytes/s for a tailnet, both directions.
    func rate(_ name: String) -> (rx: Double, tx: Double) {
        guard let s = stats[name] else { return (0, 0) }
        return (s.rx?.last ?? 0, s.tx?.last ?? 0)
    }

    var totalRate: (rx: Double, tx: Double) {
        tailnets.reduce((0, 0)) { acc, t in
            let r = rate(t.name)
            return (acc.0 + r.rx, acc.1 + r.tx)
        }
    }

    /// Per-tailnet throughput series for the chart, newest sample at x=0.
    func series(_ name: String) -> [Double] {
        guard let s = stats[name] else { return [] }
        let rx = s.rx ?? [], tx = s.tx ?? []
        return zip(rx, tx).map { $0 + $1 }
    }
}

enum Palette {
    static let colors: [Color] = [
        Color(red: 0.25, green: 0.52, blue: 0.96),
        Color(red: 0.95, green: 0.55, blue: 0.20),
        Color(red: 0.30, green: 0.73, blue: 0.47),
        Color(red: 0.80, green: 0.35, blue: 0.75),
        Color(red: 0.93, green: 0.36, blue: 0.40),
        Color(red: 0.20, green: 0.70, blue: 0.78),
        Color(red: 0.62, green: 0.50, blue: 0.30),
        Color(red: 0.55, green: 0.55, blue: 0.95),
        Color(red: 0.60, green: 0.62, blue: 0.66),
    ]

    static func color(_ index: Int) -> Color { colors[index % colors.count] }
}

func formatRate(_ bps: Double) -> String {
    formatBytes(bps) + "/s"
}

func formatBytes(_ b: Double) -> String {
    let units = ["B", "KB", "MB", "GB", "TB"]
    var v = b
    var i = 0
    while v >= 1000 && i < units.count - 1 {
        v /= 1000
        i += 1
    }
    if i == 0 { return "\(Int(v)) B" }
    return String(format: v < 10 ? "%.1f %@" : "%.0f %@", v, units[i])
}
