import Charts
import ServiceManagement
import SwiftUI

/// The dropdown: header, live throughput chart, one row per tailnet with
/// an on/off switch, and a footer. Everything is plain SwiftUI (no
/// AppKit-backed controls) so it also renders in snapshot mode.
struct PanelView: View {
    @ObservedObject var store: Store
    var snapshot = false

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            header
            if let offline = store.offline {
                OfflineView(message: offline)
            } else {
                ThroughputChart(store: store)
                VStack(spacing: 2) {
                    ForEach(Array(store.tailnets.enumerated()), id: \.element.id) { i, t in
                        TailnetRow(tailnet: t, color: Palette.color(i), store: store)
                    }
                }
            }
            Divider().opacity(0.5)
            FooterView(snapshot: snapshot)
        }
        .padding(14)
        .frame(width: 340)
    }

    private var header: some View {
        HStack(alignment: .center, spacing: 10) {
            Image(nsImage: DotsIcon.image(lit: store.runningCount, reachable: store.offline == nil, size: 26))
                .renderingMode(.template)
                .foregroundStyle(.primary)
            VStack(alignment: .leading, spacing: 1) {
                Text("tailmux").font(.system(size: 14, weight: .semibold))
                Text(subtitle).font(.system(size: 11)).foregroundStyle(.secondary)
            }
            Spacer()
            if store.offline == nil {
                let r = store.totalRate
                VStack(alignment: .trailing, spacing: 1) {
                    Label(formatRate(r.rx), systemImage: "arrow.down")
                    Label(formatRate(r.tx), systemImage: "arrow.up")
                }
                .labelStyle(CompactLabel())
                .font(.system(size: 11).monospacedDigit())
                .foregroundStyle(.secondary)
            }
        }
    }

    private var subtitle: String {
        if store.offline != nil { return "Not running" }
        let n = store.tailnets.count
        var s = "\(store.runningCount) of \(n) tailnet\(n == 1 ? "" : "s") connected"
        if let tun = store.status?.tun { s += " · \(tun.interface)" }
        return s
    }
}

struct CompactLabel: LabelStyle {
    func makeBody(configuration: Configuration) -> some View {
        HStack(spacing: 3) {
            configuration.title
            configuration.icon.font(.system(size: 8, weight: .bold))
        }
    }
}

struct ThroughputChart: View {
    @ObservedObject var store: Store

    struct Point: Identifiable {
        var id: String { "\(name)-\(t)" }
        var name: String
        var t: Int
        var v: Double
    }

    var body: some View {
        let points = makePoints()
        let peak = points.map(\.v).max() ?? 0
        VStack(alignment: .leading, spacing: 4) {
            HStack {
                Text("Throughput").font(.system(size: 11, weight: .medium)).foregroundStyle(.secondary)
                Spacer()
                Text("last 2 min").font(.system(size: 10)).foregroundStyle(.tertiary)
            }
            Chart(points) { p in
                LineMark(x: .value("t", p.t), y: .value("rate", p.v), series: .value("tailnet", p.name))
                    .foregroundStyle(color(p.name))
                    .lineStyle(StrokeStyle(lineWidth: 1.6, lineCap: .round, lineJoin: .round))
                    .interpolationMethod(.monotone)
                AreaMark(x: .value("t", p.t), y: .value("rate", p.v), series: .value("tailnet", p.name), stacking: .unstacked)
                    .foregroundStyle(color(p.name).opacity(0.08))
                    .interpolationMethod(.monotone)
            }
            .chartXScale(domain: -(HistoryLen - 1)...0)
            .chartYScale(domain: 0...max(peak * 1.15, 2048))
            .chartPlotStyle { $0.clipped() }
            .chartXAxis(.hidden)
            .chartYAxis {
                AxisMarks(position: .trailing, values: .automatic(desiredCount: 3)) { v in
                    AxisGridLine(stroke: StrokeStyle(lineWidth: 0.5, dash: [2, 3]))
                    AxisValueLabel {
                        Text(formatRate(v.as(Double.self) ?? 0)).font(.system(size: 9))
                    }
                }
            }
            .frame(height: 96)
        }
        .padding(10)
        .background(RoundedRectangle(cornerRadius: 10).fill(Color.primary.opacity(0.04)))
    }

    private let HistoryLen = 120

    private func makePoints() -> [Point] {
        var out: [Point] = []
        for t in store.tailnets where t.enabled {
            let s = store.series(t.name)
            for (i, v) in s.enumerated() {
                out.append(Point(name: t.name, t: i - (s.count - 1), v: v))
            }
        }
        return out
    }

    private func color(_ name: String) -> Color {
        Palette.color(store.tailnets.firstIndex { $0.name == name } ?? 0)
    }
}

struct TailnetRow: View {
    var tailnet: TailnetStatus
    var color: Color
    @ObservedObject var store: Store
    // Plain ObservableObjects instead of @State: on recent SDKs @State is a
    // macro whose plugin ships only with Xcode, and this must build with
    // just the Command Line Tools (Homebrew).
    @StateObject private var hover = Flag()

    var body: some View {
        HStack(spacing: 10) {
            Circle()
                .fill(tailnet.running ? color : Color.secondary.opacity(0.35))
                .frame(width: 8, height: 8)
            VStack(alignment: .leading, spacing: 1) {
                Text(tailnet.name).font(.system(size: 13, weight: .medium))
                Text(detail).font(.system(size: 11)).foregroundStyle(detailColor).lineLimit(1)
            }
            .layoutPriority(1)
            Spacer(minLength: 6)
            if tailnet.running {
                Sparkline(values: store.series(tailnet.name).suffix(40).map { $0 }, color: color)
                    .frame(width: 40, height: 16)
                Text(formatRate(sum(store.rate(tailnet.name))))
                    .font(.system(size: 10).monospacedDigit())
                    .foregroundStyle(.secondary)
                    .frame(width: 52, alignment: .trailing)
            }
            PillSwitch(isOn: tailnet.enabled, tint: color) {
                store.setEnabled(tailnet.name, !tailnet.enabled)
            }
            .opacity(store.pending.contains(tailnet.name) ? 0.5 : 1)
        }
        .padding(.horizontal, 8)
        .padding(.vertical, 6)
        .background(RoundedRectangle(cornerRadius: 7).fill(Color.primary.opacity(hover.on ? 0.06 : 0)))
        .contentShape(Rectangle())
        .onHover { hover.on = $0 }
        .onTapGesture {
            if let u = tailnet.authUrl, !u.isEmpty, let url = URL(string: u) { NSWorkspace.shared.open(url) }
        }
        .help(help)
    }

    private func sum(_ r: (rx: Double, tx: Double)) -> Double { r.rx + r.tx }

    private var detail: String {
        if !tailnet.enabled { return "Off" }
        if let u = tailnet.authUrl, !u.isEmpty { return "Needs login · click to sign in" }
        switch tailnet.state {
        case "Running":
            var s = "\(tailnet.online)/\(tailnet.peers) online"
            let routes = tailnet.routes?.count ?? 0
            if routes > 0 { s += " · \(routes) route\(routes == 1 ? "" : "s")" }
            return s
        case "Starting", "NoState", nil, "": return "Connecting…"
        case let st?: return st
        }
    }

    private var detailColor: Color {
        if let u = tailnet.authUrl, !u.isEmpty, tailnet.enabled { return .orange }
        return .secondary
    }

    private var help: String {
        var lines = [tailnet.name + (tailnet.suffix.map { " (\($0))" } ?? "")]
        if let ips = tailnet.selfIps, !ips.isEmpty { lines.append("this device: " + ips.joined(separator: ", ")) }
        lines += tailnet.routes ?? []
        if let s = store.stats[tailnet.name] {
            lines.append("\(formatBytes(Double(s.rxTotal))) in, \(formatBytes(Double(s.txTotal))) out, \(s.conns) open")
        }
        return lines.joined(separator: "\n")
    }
}

struct Sparkline: View {
    var values: [Double]
    var color: Color

    var body: some View {
        GeometryReader { geo in
            let peak = max(values.max() ?? 0, 1)
            Path { p in
                guard values.count > 1 else { return }
                for (i, v) in values.enumerated() {
                    let x = geo.size.width * CGFloat(i) / CGFloat(values.count - 1)
                    let y = geo.size.height * (1 - CGFloat(v / peak)) * 0.9 + geo.size.height * 0.05
                    i == 0 ? p.move(to: CGPoint(x: x, y: y)) : p.addLine(to: CGPoint(x: x, y: y))
                }
            }
            .stroke(color, style: StrokeStyle(lineWidth: 1.2, lineCap: .round, lineJoin: .round))
        }
    }
}

/// A macOS-style switch drawn in SwiftUI.
struct PillSwitch: View {
    var isOn: Bool
    var tint: Color
    var action: () -> Void

    var body: some View {
        ZStack(alignment: isOn ? .trailing : .leading) {
            Capsule().fill(isOn ? tint : Color.primary.opacity(0.15))
            Circle()
                .fill(.white)
                .shadow(color: .black.opacity(0.25), radius: 0.8, y: 0.5)
                .padding(2)
        }
        .frame(width: 30, height: 18)
        .animation(.easeOut(duration: 0.15), value: isOn)
        .contentShape(Capsule())
        .onTapGesture(perform: action)
        .accessibilityAddTraits(.isButton)
        .accessibilityValue(isOn ? "on" : "off")
    }
}

struct OfflineView: View {
    var message: String

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            Text("The tailmux daemon isn't reachable.").font(.system(size: 12, weight: .medium))
            Text("Start it with `tailmux up`, or `sudo brew services start tailmux` for TUN mode.")
                .font(.system(size: 11)).foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
            Text(message).font(.system(size: 10)).foregroundStyle(.tertiary).lineLimit(2)
        }
        .padding(10)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(RoundedRectangle(cornerRadius: 10).fill(Color.primary.opacity(0.04)))
    }
}

struct FooterView: View {
    var snapshot: Bool
    @StateObject private var loginItem = Flag(SMAppService.mainApp.status == .enabled)

    var body: some View {
        HStack {
            HStack(spacing: 8) {
                PillSwitch(isOn: loginItem.on, tint: .accentColor) { toggleLogin() }
                    .scaleEffect(0.85)
                Text("Open at login").font(.system(size: 12))
            }
            Spacer()
            Text("Quit")
                .font(.system(size: 12))
                .foregroundStyle(.secondary)
                .contentShape(Rectangle())
                .onTapGesture { NSApp.terminate(nil) }
        }
    }

    private func toggleLogin() {
        guard !snapshot else { return }
        do {
            if loginItem.on { try SMAppService.mainApp.unregister() } else { try SMAppService.mainApp.register() }
        } catch {
            NSLog("tailmux: login item: \(error)")
        }
        loginItem.on = SMAppService.mainApp.status == .enabled
    }
}

final class Flag: ObservableObject {
    @Published var on: Bool
    init(_ on: Bool = false) { self.on = on }
}
