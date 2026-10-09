import AppKit
import SwiftUI

/// The full app: sidebar with Overview, Tailnets, Devices, Exit node,
/// Settings, Logs.
struct MainWindow: View {
    @ObservedObject var store: Store
    @ObservedObject var manager: Manager

    var body: some View {
        // A hand-built sidebar instead of NavigationSplitView: that one
        // always draws a divider and tints the icons.
        HStack(spacing: 0) {
            Sidebar(store: store, manager: manager)
                .frame(width: 210)
                .background(SidebarMaterial().ignoresSafeArea())
            VStack(spacing: 0) {
                if let offline = store.offline {
                    OfflineView(message: offline).padding(20)
                    Spacer()
                } else {
                    switch manager.page {
                    case .overview: OverviewPage(store: store, manager: manager)
                    case .tailnets: TailnetsPage(store: store, manager: manager)
                    case .devices: DevicesPage(manager: manager)
                    case .exitNode: ExitNodePage(store: store, manager: manager)
                    case .settings: SettingsPage(store: store, manager: manager)
                    case .logs: LogsPage(manager: manager)
                    }
                }
                StatusLine(manager: manager)
            }
            .frame(minWidth: 560, maxWidth: .infinity, maxHeight: .infinity)
            .background(Color(nsColor: .windowBackgroundColor))
        }
        .ignoresSafeArea(.container, edges: .top)
    }
}

struct Sidebar: View {
    @ObservedObject var store: Store
    @ObservedObject var manager: Manager

    var body: some View {
        VStack(alignment: .leading, spacing: 2) {
            HStack(spacing: 9) {
                Image(nsImage: DotsIcon.image(lit: store.runningCount, reachable: store.offline == nil, size: 22))
                    .renderingMode(.template)
                    .foregroundStyle(.primary)
                Wordmark(size: 20)
            }
            .padding(.horizontal, 14)
            .padding(.top, 48) // clear of the traffic-light buttons
            .padding(.bottom, 18)
            ForEach(Page.allCases) { page in
                SidebarItem(page: page, selected: manager.page == page) { manager.page = page }
            }
            Spacer()
        }
        .padding(.horizontal, 10)
    }
}

struct SidebarItem: View {
    var page: Page
    var selected: Bool
    var action: () -> Void
    @StateObject private var hover = Flag()

    var body: some View {
        HStack(spacing: 10) {
            Image(systemName: page.icon)
                .font(.system(size: 14))
                .frame(width: 20)
            Text(page.title).font(.system(size: 14, weight: selected ? .semibold : .regular))
            Spacer()
        }
        .foregroundStyle(.primary) // icons the same color as the labels
        .padding(.horizontal, 10).padding(.vertical, 7)
        .background(
            RoundedRectangle(cornerRadius: 7)
                .fill(Color.primary.opacity(selected ? 0.12 : (hover.on ? 0.05 : 0)))
        )
        .contentShape(Rectangle())
        .onHover { hover.on = $0 }
        .onTapGesture(perform: action)
    }
}

/// The translucent sidebar background macOS uses.
struct SidebarMaterial: NSViewRepresentable {
    func makeNSView(context: Context) -> NSVisualEffectView {
        let v = NSVisualEffectView()
        v.material = .sidebar
        v.blendingMode = .behindWindow
        v.state = .followsWindowActiveState
        return v
    }

    func updateNSView(_ v: NSVisualEffectView, context: Context) {}
}

struct StatusLine: View {
    @ObservedObject var manager: Manager

    var body: some View {
        if manager.busy || manager.error != nil || manager.message != nil {
            HStack(spacing: 8) {
                if manager.busy { ProgressView().controlSize(.small) }
                if let e = manager.error {
                    Image(systemName: "exclamationmark.triangle.fill").foregroundStyle(.orange)
                    Text(e).lineLimit(2)
                } else if let m = manager.message {
                    Text(m).foregroundStyle(.secondary)
                }
                Spacer()
            }
            .font(.system(size: 12))
            .padding(.horizontal, 20).padding(.vertical, 8)
            .background(.bar)
        }
    }
}

struct PageHeader<Trailing: View>: View {
    var title: String
    var subtitle: String
    @ViewBuilder var trailing: Trailing

    var body: some View {
        HStack(alignment: .firstTextBaseline) {
            VStack(alignment: .leading, spacing: 2) {
                Text(title).font(.system(size: 22, weight: .bold))
                Text(subtitle).font(.system(size: 12)).foregroundStyle(.secondary)
            }
            Spacer()
            trailing
        }
        .padding(.horizontal, 24).padding(.top, 18).padding(.bottom, 12)
    }
}

// MARK: Overview

struct OverviewPage: View {
    @ObservedObject var store: Store
    @ObservedObject var manager: Manager

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                PageHeader(title: "Overview", subtitle: subtitle) { EmptyView() }
                    .padding(.horizontal, -24)
                if let u = store.status?.update, u.available || u.state == "installing" || u.state == "failed" {
                    UpdateBanner(info: u, store: store)
                }
                HStack(spacing: 12) {
                    StatCard(label: "Tailnets connected", value: "\(store.runningCount) of \(store.tailnets.count)")
                    StatCard(label: "Devices online", value: "\(manager.peers.filter(\.online).count) of \(manager.peers.count)")
                    StatCard(label: "Downloading", value: formatRate(store.totalRate.rx))
                    StatCard(label: "Uploading", value: formatRate(store.totalRate.tx))
                }
                ThroughputChart(store: store, height: 200)
                VStack(spacing: 2) {
                    ForEach(Array(store.tailnets.enumerated()), id: \.element.id) { i, t in
                        TailnetRow(tailnet: t, color: Palette.color(i), store: store)
                    }
                    if !store.exitNodes.isEmpty || store.exitNode != nil {
                        Divider().padding(.horizontal, 8).padding(.vertical, 4)
                        ExitNodeRow(store: store)
                    }
                }
            }
            .padding(.horizontal, 24).padding(.bottom, 24)
        }
    }

    private var subtitle: String {
        var s = "tailmux \(store.status?.version ?? "")"
        if let tun = store.status?.tun {
            s += " · TUN on \(tun.interface)"
        } else {
            s += " · proxy mode"
        }
        return s
    }
}

struct StatCard: View {
    var label: String
    var value: String

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(label).font(.system(size: 11)).foregroundStyle(.secondary)
            Text(value).font(.system(size: 20, weight: .semibold).monospacedDigit())
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(14)
        .background(RoundedRectangle(cornerRadius: 10).fill(Color.primary.opacity(0.04)))
    }
}

// MARK: Tailnets

struct TailnetsPage: View {
    @ObservedObject var store: Store
    @ObservedObject var manager: Manager
    @StateObject private var confirm = Pending()

    var body: some View {
        VStack(spacing: 0) {
            PageHeader(title: "Tailnets", subtitle: "Each one is joined as its own device, all at once.") {
                Button { manager.showAdd = true } label: { Label("Add tailnet", systemImage: "plus") }
                    .buttonStyle(.borderedProminent)
            }
            ScrollView {
                VStack(spacing: 10) {
                    ForEach(Array(store.tailnets.enumerated()), id: \.element.id) { i, t in
                        TailnetCard(tailnet: t, color: Palette.color(i), store: store, manager: manager,
                                    onLogout: { confirm.ask(t.name, .logout) },
                                    onRemove: { confirm.ask(t.name, .remove) })
                    }
                    if store.tailnets.isEmpty {
                        Text("No tailnets yet. Add one to get started.")
                            .foregroundStyle(.secondary).padding(40)
                    }
                }
                .padding(.horizontal, 24).padding(.bottom, 24)
            }
        }
        .sheet(isPresented: $manager.showAdd) { AddTailnetSheet(manager: manager) }
        .confirmationDialog(confirm.title, isPresented: Binding(get: { confirm.name != nil }, set: { if !$0 { confirm.name = nil } })) {
            Button(confirm.kind == .logout ? "Log out" : "Remove", role: .destructive) {
                if let n = confirm.name {
                    if confirm.kind == .logout { manager.logout(n) } else { manager.remove(n) }
                }
                confirm.name = nil
            }
        } message: {
            if confirm.kind == .logout {
                Text("This device leaves the tailnet; you'll need to log in again.")
            } else {
                Text("Removes \(confirm.name ?? "") and signs this device out of it. Adding it back needs a new login.")
            }
        }
    }
}

final class Pending: ObservableObject {
    enum Kind { case remove, logout }
    @Published var name: String?
    @Published var kind = Kind.remove

    func ask(_ name: String, _ kind: Kind) {
        self.kind = kind
        self.name = name
    }

    var title: String {
        kind == .logout ? "Log out of \(name ?? "")?" : "Remove \(name ?? "")?"
    }
}

struct TailnetCard: View {
    var tailnet: TailnetStatus
    var color: Color
    @ObservedObject var store: Store
    @ObservedObject var manager: Manager
    var onLogout: () -> Void
    var onRemove: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack(spacing: 10) {
                Circle().fill(tailnet.running ? color : Color.secondary.opacity(0.35)).frame(width: 10, height: 10)
                Text(tailnet.name).font(.system(size: 15, weight: .semibold))
                Text(state).font(.system(size: 12)).foregroundStyle(stateColor)
                Spacer()
                if let u = tailnet.authUrl, !u.isEmpty {
                    Button("Log in…") { manager.openLogin(u) }.buttonStyle(.borderedProminent)
                }
                PillSwitch(isOn: tailnet.enabled, tint: color) { store.setEnabled(tailnet.name, !tailnet.enabled) }
                Menu {
                    if let s = tailnet.suffix, !s.isEmpty { Button("Copy MagicDNS suffix") { manager.copy(s) } }
                    if let ip = tailnet.selfIps?.first { Button("Copy this device's IP") { manager.copy(ip) } }
                    Divider()
                    if (tailnet.authUrl ?? "").isEmpty { Button("Log out…", action: onLogout) }
                    Button("Remove…", role: .destructive, action: onRemove)
                } label: {
                    Image(systemName: "ellipsis.circle")
                }
                .menuStyle(.borderlessButton).menuIndicator(.hidden).fixedSize()
            }
            HStack(spacing: 18) {
                Detail(label: "MagicDNS", value: tailnet.suffix ?? "–")
                Detail(label: "This device", value: tailnet.selfIps?.first ?? "–")
                Detail(label: "Devices", value: "\(tailnet.online)/\(tailnet.peers) online")
                Detail(label: "Routes", value: "\(tailnet.routes?.count ?? 0)")
                if let s = store.stats[tailnet.name] {
                    Detail(label: "Transferred", value: formatBytes(Double(s.rxTotal + s.txTotal)))
                }
            }
            if tailnet.needsSignature, let cmd = tailnet.lock?.signCommand {
                VStack(alignment: .leading, spacing: 6) {
                    Text("This tailnet uses Tailnet Lock, and its devices ignore this one until it's signed. Run this on a signing device:")
                        .font(.system(size: 12)).foregroundStyle(.orange)
                    HStack(spacing: 8) {
                        Text(cmd).font(.system(size: 11, design: .monospaced)).textSelection(.enabled).lineLimit(2)
                        Spacer()
                        Button("Copy") { manager.copy(cmd) }
                    }
                }
            }
            if let routes = tailnet.routes, !routes.isEmpty {
                Text(routes.joined(separator: "   "))
                    .font(.system(size: 11, design: .monospaced)).foregroundStyle(.secondary)
                    .lineLimit(2)
            }
        }
        .padding(16)
        .background(RoundedRectangle(cornerRadius: 12).fill(Color.primary.opacity(0.04)))
    }

    private var state: String {
        if !tailnet.enabled { return "Off" }
        if let u = tailnet.authUrl, !u.isEmpty { return "Needs login" }
        if tailnet.needsSignature { return "Needs signing" }
        switch tailnet.state {
        case "Running": return "Connected"
        case "Starting", "NoState", nil, "": return "Connecting…"
        case let s?: return s
        }
    }

    private var stateColor: Color {
        if let u = tailnet.authUrl, !u.isEmpty, tailnet.enabled { return .orange }
        if tailnet.needsSignature, tailnet.enabled { return .orange }
        return tailnet.running ? .green : .secondary
    }
}

struct Detail: View {
    var label: String
    var value: String

    var body: some View {
        VStack(alignment: .leading, spacing: 2) {
            Text(label).font(.system(size: 10)).foregroundStyle(.secondary)
            Text(value).font(.system(size: 12).monospacedDigit()).textSelection(.enabled)
        }
    }
}

struct AddTailnetSheet: View {
    @ObservedObject var manager: Manager

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text("Add a tailnet").font(.system(size: 18, weight: .bold))
            if let url = manager.addedLoginURL {
                Text("Almost there: sign in to this tailnet with the account that owns it.")
                Text(url).font(.system(size: 11, design: .monospaced)).foregroundStyle(.secondary).textSelection(.enabled)
                HStack {
                    Spacer()
                    Button("Done") { manager.addedLoginURL = nil; manager.showAdd = false }
                    Button("Open login page") { manager.openLogin(url) }.buttonStyle(.borderedProminent)
                }
            } else {
                Form {
                    TextField("Name", text: $manager.newName, prompt: Text("work"))
                    TextField("Control server", text: $manager.newControl, prompt: Text("Tailscale (leave empty) or a Headscale URL"))
                    SecureField("Auth key", text: $manager.newKey, prompt: Text("optional; empty logs in through the browser"))
                }
                .formStyle(.grouped)
                Text("The name is also a DNS suffix: devices become host.name.")
                    .font(.system(size: 11)).foregroundStyle(.secondary)
                if let e = manager.error {
                    Text(e).font(.system(size: 12)).foregroundStyle(.orange)
                }
                HStack {
                    Spacer()
                    Button("Cancel") { manager.showAdd = false }.keyboardShortcut(.cancelAction)
                    Button("Add") { manager.addTailnet() }
                        .buttonStyle(.borderedProminent).keyboardShortcut(.defaultAction)
                        .disabled(manager.newName.trimmingCharacters(in: .whitespaces).isEmpty || manager.busy)
                }
            }
        }
        .padding(24)
        .frame(width: 460)
    }
}

// MARK: Devices

struct DevicesPage: View {
    @ObservedObject var manager: Manager

    var body: some View {
        VStack(spacing: 0) {
            PageHeader(title: "Devices", subtitle: "Every device in every tailnet, by the name to reach it.") {
                TextField("Search", text: $manager.query, prompt: Text("Search names and IPs"))
                    .textFieldStyle(.roundedBorder).frame(width: 240)
            }
            ScrollView {
                LazyVStack(spacing: 0) {
                    ForEach(manager.filteredPeers) { p in
                        DeviceRow(peer: p, manager: manager)
                        Divider().opacity(0.4)
                    }
                    if manager.filteredPeers.isEmpty {
                        Text(manager.peers.isEmpty ? "No devices yet." : "Nothing matches.")
                            .foregroundStyle(.secondary).padding(40)
                    }
                }
                .padding(.horizontal, 24).padding(.bottom, 24)
            }
        }
    }
}

struct DeviceRow: View {
    var peer: PeerInfo
    @ObservedObject var manager: Manager

    var body: some View {
        HStack(spacing: 12) {
            Circle().fill(peer.online ? Color.green : Color.secondary.opacity(0.35)).frame(width: 7, height: 7)
            Text(peer.alias).font(.system(size: 13, design: .monospaced)).lineLimit(1)
            Spacer(minLength: 12)
            if let r = peer.routes, !r.isEmpty {
                Text("routes \(r.joined(separator: ", "))").font(.system(size: 11)).foregroundStyle(.secondary).lineLimit(1)
            }
            Text(peer.ips?.first ?? "").font(.system(size: 12, design: .monospaced)).foregroundStyle(.secondary)
                .frame(width: 120, alignment: .trailing)
            Text(peer.online ? "online" : "offline").font(.system(size: 11))
                .foregroundStyle(peer.online ? Color.green : Color.secondary).frame(width: 50, alignment: .trailing)
        }
        .padding(.vertical, 8)
        .contentShape(Rectangle())
        .onTapGesture(count: 2) { manager.copy(peer.alias) }
        .contextMenu {
            Button("Copy name (\(peer.alias))") { manager.copy(peer.alias) }
            if let ip = peer.ips?.first { Button("Copy IP (\(ip))") { manager.copy(ip) } }
            Button("Copy full name (\(peer.fqdn))") { manager.copy(peer.fqdn) }
        }
        .help("Double-click to copy \(peer.alias)")
    }
}

// MARK: Exit node

struct ExitNodePage: View {
    @ObservedObject var store: Store
    @ObservedObject var manager: Manager

    var body: some View {
        let catalog = ExitCatalog(store.exitNodes, query: manager.exitQuery)
        VStack(spacing: 0) {
            PageHeader(title: "Exit node", subtitle: "Send everything no tailnet claims through one device.") {
                TextField("Search", text: $manager.exitQuery, prompt: Text("Search names, countries, cities"))
                    .textFieldStyle(.roundedBorder).frame(width: 240)
            }
            ScrollView {
                VStack(alignment: .leading, spacing: 10) {
                    ExitCurrentCard(store: store, manager: manager)
                    LazyVStack(alignment: .leading, spacing: 0) {
                        if manager.exitQuery.isEmpty {
                            ExitPickRow(title: "None", detail: "Traffic no tailnet claims goes direct", selected: store.exitNode == nil) {
                                manager.setExitNode(nil)
                            }
                        }
                        ForEach(catalog.own) { g in
                            VStack(alignment: .leading, spacing: 0) {
                                ExitSectionLabel(title: g.tailnet)
                                ForEach(g.nodes) { n in
                                    ExitPickRow(node: n, detail: n.ips?.first ?? "") { manager.setExitNode(n) }
                                }
                            }
                        }
                        ForEach(catalog.located) { p in
                            ExitSectionLabel(title: p.title)
                            ForEach(p.countries) { c in
                                ExitCountryRows(country: c, manager: manager, open: isOpen(c))
                            }
                        }
                        if catalog.isEmpty {
                            Text(store.exitNodes.isEmpty ? "No device in your tailnets offers itself as an exit node." : "Nothing matches.")
                                .foregroundStyle(.secondary).padding(40)
                                .frame(maxWidth: .infinity)
                        }
                    }
                }
                .padding(.horizontal, 24).padding(.bottom, 24)
            }
        }
    }

    private func isOpen(_ c: ExitCatalog.Country) -> Bool {
        if !manager.exitQuery.isEmpty { return true }
        return c.nodes.contains(where: \.isSelected) != manager.toggled.contains(c.id)
    }
}

/// What's chosen, whether traffic can use it, and what it covers.
struct ExitCurrentCard: View {
    @ObservedObject var store: Store
    @ObservedObject var manager: Manager

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack(spacing: 10) {
                Circle().fill(dotColor).frame(width: 10, height: 10)
                Text(store.exitNode?.place ?? "No exit node").font(.system(size: 15, weight: .semibold))
                Text(state).font(.system(size: 12)).foregroundStyle(stateColor)
                Spacer()
                if store.exitNode != nil {
                    Button("Turn off") { manager.setExitNode(nil) }.disabled(manager.busy)
                }
            }
            Text(explanation)
                .font(.system(size: 12)).foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
            if let x = store.exitNode {
                if !x.active {
                    HStack(alignment: .firstTextBaseline, spacing: 6) {
                        Image(systemName: "exclamationmark.triangle.fill").foregroundStyle(.orange)
                        Text((x.error ?? "Not available") + ". That traffic is blocked, not sent direct.")
                            .fixedSize(horizontal: false, vertical: true)
                    }
                    .font(.system(size: 12))
                }
                HStack(spacing: 18) {
                    Detail(label: "Device", value: x.displayName)
                    Detail(label: "Tailnet", value: x.tailnet)
                    if let l = x.location {
                        Detail(label: "Location", value: [l.city, l.country].compactMap { $0 }.joined(separator: ", "))
                    }
                    Detail(label: "Covers", value: store.status?.tun != nil ? "Every app (TUN)" : "Apps using the proxy")
                }
            }
        }
        .padding(16)
        .background(RoundedRectangle(cornerRadius: 12).fill(Color.primary.opacity(0.04)))
    }

    private var state: String {
        guard let x = store.exitNode else { return "Off" }
        return x.active ? "Active" : "Not working"
    }

    private var dotColor: Color {
        guard let x = store.exitNode else { return Color.secondary.opacity(0.35) }
        return x.active ? .green : .orange
    }

    private var stateColor: Color {
        guard let x = store.exitNode else { return .secondary }
        return x.active ? .green : .orange
    }

    private var explanation: String {
        guard let x = store.exitNode else {
            return "Traffic no tailnet claims goes straight to the internet. Pick a device below to send it through that device instead."
        }
        let covers = store.status?.tun != nil ? "every app (TUN mode)" : "only apps using the tailmux proxy"
        return "Everything no tailnet claims leaves through \(x.displayName), for \(covers). Your local network stays direct."
    }
}

struct ExitSectionLabel: View {
    var title: String

    var body: some View {
        Text(title).font(.system(size: 11, weight: .semibold)).foregroundStyle(.secondary)
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(.top, 14).padding(.bottom, 4)
    }
}

/// A country: a header with "Best available" that opens to its cities.
struct ExitCountryRows: View {
    var country: ExitCatalog.Country
    @ObservedObject var manager: Manager
    var open: Bool

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            rows
        }
    }

    @ViewBuilder private var rows: some View {
        ExitGroupHeader(title: country.title, count: country.nodes.count, open: open, selected: country.nodes.contains(where: \.isSelected),
                        best: ExitCatalog.best(country.nodes), manager: manager) {
            if manager.toggled.contains(country.id) { manager.toggled.remove(country.id) } else { manager.toggled.insert(country.id) }
        }
        Divider().opacity(0.4)
        if open {
            ForEach(country.cities) { city in
                let header = country.cities.count > 1 && city.nodes.count > 1
                if header {
                    ExitGroupHeader(title: city.name, count: city.nodes.count, open: nil, selected: false,
                                    best: ExitCatalog.best(city.nodes), manager: manager, toggle: nil)
                        .padding(.leading, 18)
                    Divider().opacity(0.4)
                }
                ForEach(city.nodes) { n in
                    ExitPickRow(node: n, detail: header ? n.ips?.first ?? "" : city.name) { manager.setExitNode(n) }
                        .padding(.leading, 18)
                }
            }
        }
    }
}

struct ExitGroupHeader: View {
    var title: String
    var count: Int
    var open: Bool?
    var selected: Bool
    var best: ExitNodeInfo?
    @ObservedObject var manager: Manager
    var toggle: (() -> Void)?

    var body: some View {
        HStack(spacing: 10) {
            if let open {
                Image(systemName: "chevron.right")
                    .font(.system(size: 10, weight: .semibold)).foregroundStyle(.secondary)
                    .rotationEffect(.degrees(open ? 90 : 0))
                    .frame(width: 12)
            }
            Text(title).font(.system(size: 13, weight: open == nil ? .regular : .medium))
            Text("\(count)").font(.system(size: 11).monospacedDigit()).foregroundStyle(.secondary)
            if selected { Image(systemName: "checkmark").font(.system(size: 11, weight: .semibold)).foregroundStyle(Color.accentColor) }
            Spacer()
            if let best {
                Button("Best available") { manager.setExitNode(best) }
                    .controlSize(.small).disabled(manager.busy)
                    .help("Picks \(best.name): the highest priority one online")
            }
        }
        .padding(.vertical, 7)
        .contentShape(Rectangle())
        .onTapGesture { toggle?() }
    }
}

struct ExitPickRow: View {
    var title: String
    var detail: String
    var selected: Bool
    var online: Bool?
    var help: String?
    var action: () -> Void
    @StateObject private var hover = Flag()

    init(title: String, detail: String, selected: Bool, action: @escaping () -> Void) {
        self.title = title
        self.detail = detail
        self.selected = selected
        self.action = action
    }

    init(node n: ExitNodeInfo, detail: String, action: @escaping () -> Void) {
        title = n.name
        self.detail = detail
        selected = n.isSelected
        online = n.online
        help = ([n.fqdn] + (n.ips ?? [])).joined(separator: "\n")
        self.action = action
    }

    var body: some View {
        HStack(spacing: 12) {
            Image(systemName: "checkmark")
                .font(.system(size: 11, weight: .semibold)).foregroundStyle(Color.accentColor)
                .frame(width: 12).opacity(selected ? 1 : 0)
            if let online {
                Circle().fill(online ? Color.green : Color.secondary.opacity(0.35)).frame(width: 7, height: 7)
            }
            Text(title).font(.system(size: 13, design: online == nil ? .default : .monospaced)).lineLimit(1)
            Spacer(minLength: 12)
            Text(detail).font(.system(size: 12, design: online == nil ? .default : .monospaced)).foregroundStyle(.secondary).lineLimit(1)
        }
        .padding(.vertical, 7).padding(.horizontal, 6)
        .background(RoundedRectangle(cornerRadius: 6).fill(Color.primary.opacity(hover.on ? 0.05 : 0)))
        .contentShape(Rectangle())
        .onHover { hover.on = $0 }
        .onTapGesture(perform: action)
        .help(help ?? "")
    }
}

// MARK: Settings

struct SettingsPage: View {
    @ObservedObject var store: Store
    @ObservedObject var manager: Manager

    var body: some View {
        VStack(spacing: 0) {
            PageHeader(title: "Settings", subtitle: "Changes to the service apply after a restart.") {
                Button("Save") { manager.saveSettings() }
                    .buttonStyle(.borderedProminent).disabled(manager.busy)
            }
            if manager.restartPending {
                HStack {
                    Image(systemName: "arrow.clockwise.circle.fill").foregroundStyle(Color.accentColor)
                    Text("Saved. Restart the service to apply your changes.")
                    Spacer()
                    Button("Restart now") { manager.restartService() }
                }
                .padding(12)
                .background(RoundedRectangle(cornerRadius: 10).fill(Color.accentColor.opacity(0.08)))
                .padding(.horizontal, 24)
            }
            Form {
                Section("This device") {
                    TextField("Device name", text: $manager.hostname, prompt: Text("tailmux-\(Host.current().localizedName ?? "mac")"))
                    Text("How this Mac shows up in every tailnet.").font(.caption).foregroundStyle(.secondary)
                }
                Section("Network") {
                    Toggle("TUN mode: reach tailnets from every app", isOn: $manager.tun)
                    Text("Needs the service to run as root: sudo brew services start tailmux. Off leaves only the SOCKS5 and HTTP proxies.")
                        .font(.caption).foregroundStyle(.secondary)
                    HStack {
                        Button("Repair network") { manager.repair() }
                        Text("Re-applies routes and DNS and flushes the DNS cache.").font(.caption).foregroundStyle(.secondary)
                    }
                }
                Section("Updates") {
                    Toggle("Install updates automatically", isOn: $manager.autoUpdate)
                    HStack {
                        Text(updateLine).foregroundStyle(.secondary)
                        Spacer()
                        Button("Check now") { manager.checkForUpdates() }
                    }
                }
                Section("App") {
                    Toggle("Open at login", isOn: Binding(get: { manager.loginItem }, set: { manager.setLoginItem($0) }))
                    HStack {
                        Button("Restart service") { manager.restartService() }
                        Button("Quit menu bar app") { NSApp.terminate(nil) }
                    }
                }
                if let c = manager.config {
                    Section("Files") {
                        LabeledContent("Config") { PathLink(path: c.path) }
                        LabeledContent("State") { PathLink(path: c.stateDir) }
                    }
                }
            }
            .formStyle(.grouped)
        }
    }

    private var updateLine: String {
        guard let u = store.status?.update else { return "tailmux \(store.status?.version ?? "")" }
        if u.available { return "tailmux \(u.latest ?? "") is available (you have \(u.current))" }
        return "tailmux \(u.current) is up to date"
    }
}

struct PathLink: View {
    var path: String

    var body: some View {
        HStack(spacing: 6) {
            Text(path).font(.system(size: 11, design: .monospaced)).textSelection(.enabled).lineLimit(1).truncationMode(.middle)
            Button {
                NSWorkspace.shared.activateFileViewerSelecting([URL(fileURLWithPath: path)])
            } label: { Image(systemName: "arrow.right.circle") }
                .buttonStyle(.borderless).help("Show in Finder")
        }
    }
}

// MARK: Logs

struct LogsPage: View {
    @ObservedObject var manager: Manager

    var body: some View {
        VStack(spacing: 0) {
            PageHeader(title: "Logs", subtitle: "The service's recent activity, newest at the bottom.") {
                Button("Copy all") { manager.copy(manager.logs.joined(separator: "\n")) }
            }
            ScrollViewReader { proxy in
                ScrollView {
                    LazyVStack(alignment: .leading, spacing: 1) {
                        ForEach(Array(manager.logs.enumerated()), id: \.offset) { i, line in
                            Text(line).font(.system(size: 11, design: .monospaced))
                                .foregroundStyle(line.contains("fail") || line.contains("error") ? Color.orange : Color.primary)
                                .textSelection(.enabled)
                                .frame(maxWidth: .infinity, alignment: .leading)
                                .id(i)
                        }
                    }
                    .padding(.horizontal, 24).padding(.bottom, 16)
                }
                .onChange(of: manager.logs.count) { n in
                    if n > 0 { proxy.scrollTo(n - 1, anchor: .bottom) }
                }
            }
        }
    }
}
