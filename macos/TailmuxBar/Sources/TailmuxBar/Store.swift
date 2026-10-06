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
    @Published var exitNodes: [ExitNodeInfo] = []
    @Published var exitPending = false
    @Published var exitError: String?

    let api: API
    private var timer: Timer?
    private var ticks = 0

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
            exitNodes = []
            offline = error.localizedDescription
        }
        // The exit node list can run to hundreds of Mullvad nodes and
        // rarely changes; every five seconds is plenty.
        if offline == nil && ticks % 5 == 0 { await refreshExitNodes() }
        ticks += 1
    }

    func refreshExitNodes() async {
        guard let r = try? await api.get("exit-nodes", as: ExitNodesResponse.self) else { return }
        exitNodes = r.nodes ?? []
    }

    func startUpdate() {
        Task {
            do { try await api.startUpdate() } catch { offline = error.localizedDescription }
            await refresh()
        }
    }

    /// Homebrew upgrades the app together with the daemon. Once the
    /// daemon is running a version this app isn't, relaunch from the
    /// installed bundle that matches it: this one's own location first
    /// (the daemon refreshes the /Applications copy as it starts).
    private func relaunchIfUpgraded(daemonVersion: String?) {
        guard let dv = daemonVersion, dv != "dev", !relaunching,
              let mine = Bundle.main.infoDictionary?["CFBundleShortVersionString"] as? String,
              mine != dv, mine != "ci", !mine.hasPrefix("0.0.0")
        else { return }
        let candidates = [Bundle.main.bundlePath, "/Applications/TailmuxBar.app",
                          "/opt/homebrew/opt/tailmux/TailmuxBar.app", "/usr/local/opt/tailmux/TailmuxBar.app"]
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

    /// Picks an exit node, or turns it off with nil. Applies live.
    func setExitNode(_ node: ExitNodeInfo?) {
        exitPending = true
        exitError = nil
        // Move the checkmark right away; the refresh confirms it.
        for i in exitNodes.indices { exitNodes[i].selected = exitNodes[i].key == node?.key }
        Task {
            do { try await api.setExitNode(tailnet: node?.tailnet, node: node?.spec) } catch { exitError = error.localizedDescription }
            exitPending = false
            await refresh()
            await refreshExitNodes()
        }
    }

    // MARK: derived

    var exitNode: ExitStatus? { status?.exitNode }

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

/// The exit node list grouped the way the pickers show it: your own
/// devices per tailnet, then located (Mullvad) nodes by country and city.
/// The daemon already sorts the list in that order.
struct ExitCatalog {
    struct Group: Identifiable {
        var id: String { tailnet }
        var tailnet: String
        var nodes: [ExitNodeInfo]
    }

    struct City: Identifiable {
        var id: String
        var name: String
        var nodes: [ExitNodeInfo]
    }

    struct Country: Identifiable {
        var id: String
        var name: String
        var flag: String
        var cities: [City]
        var nodes: [ExitNodeInfo] { cities.flatMap(\.nodes) }
        var title: String { flag.isEmpty ? name : flag + " " + name }
    }

    struct Provider: Identifiable {
        var id: String { tailnet }
        var tailnet: String
        var title: String
        var countries: [Country]
    }

    var own: [Group] = []
    var located: [Provider] = []

    var isEmpty: Bool { own.isEmpty && located.isEmpty }

    init(_ nodes: [ExitNodeInfo], query: String = "") {
        let q = query.trimmingCharacters(in: .whitespaces).lowercased()
        let shown = q.isEmpty ? nodes : nodes.filter { n in
            [n.name, n.fqdn, n.tailnet, n.location?.country ?? "", n.location?.city ?? ""]
                .contains { $0.lowercased().contains(q) }
        }
        for n in shown where n.location == nil {
            if own.last?.tailnet == n.tailnet { own[own.count - 1].nodes.append(n) } else { own.append(Group(tailnet: n.tailnet, nodes: [n])) }
        }
        var tailnets: [String] = []
        for n in shown where n.location != nil && !tailnets.contains(n.tailnet) { tailnets.append(n.tailnet) }
        for t in tailnets {
            let ns = shown.filter { $0.tailnet == t && $0.location != nil }
            var countries: [Country] = []
            for n in ns {
                let l = n.location!
                let country = l.country ?? l.countryCode ?? "Elsewhere"
                let city = l.city ?? l.cityCode ?? country
                if countries.last?.name != country {
                    countries.append(Country(id: t + "/" + country, name: country, flag: l.flag, cities: []))
                }
                let ci = countries.count - 1
                if countries[ci].cities.last?.name == city {
                    countries[ci].cities[countries[ci].cities.count - 1].nodes.append(n)
                } else {
                    countries[ci].cities.append(City(id: countries[ci].id + "/" + city, name: city, nodes: [n]))
                }
            }
            var title = ns.contains(where: \.isMullvad) ? "Mullvad" : "By location"
            if tailnets.count > 1 { title += " · " + t }
            located.append(Provider(tailnet: t, title: title, countries: countries))
        }
    }

    /// The node "Best available" picks: the highest priority among the
    /// online ones. Mullvad nodes may not report presence, so with none
    /// online it's just the highest priority.
    static func best(_ nodes: [ExitNodeInfo]) -> ExitNodeInfo? {
        let pool = nodes.contains(where: \.online) ? nodes.filter(\.online) : nodes
        return pool.max { $0.priority < $1.priority }
    }
}

extension ExitStatus {
    /// "🇸🇪 Stockholm" for located nodes, else the device name.
    var place: String {
        guard let l = location, let city = l.city ?? l.country else { return displayName }
        return l.flag.isEmpty ? city : l.flag + " " + city
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
