import AppKit
import SwiftUI

/// The full app: sidebar with Overview, Tailnets, Devices, Settings, Logs.
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
                        TailnetCard(tailnet: t, color: Palette.color(i), store: store, manager: manager) {
                            confirm.name = t.name
                        }
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
        .confirmationDialog("Remove \(confirm.name ?? "")?", isPresented: Binding(get: { confirm.name != nil }, set: { if !$0 { confirm.name = nil } })) {
            Button("Remove", role: .destructive) {
                if let n = confirm.name { manager.remove(n) }
                confirm.name = nil
            }
        } message: {
            Text("tailmux leaves this tailnet and forgets it. Its login is kept, so adding it back needs no new login.")
        }
    }
}

final class Pending: ObservableObject {
    @Published var name: String?
}

struct TailnetCard: View {
    var tailnet: TailnetStatus
    var color: Color
    @ObservedObject var store: Store
    @ObservedObject var manager: Manager
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
        switch tailnet.state {
        case "Running": return "Connected"
        case "Starting", "NoState", nil, "": return "Connecting…"
        case let s?: return s
        }
    }

    private var stateColor: Color {
        if let u = tailnet.authUrl, !u.isEmpty, tailnet.enabled { return .orange }
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
