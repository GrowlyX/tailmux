import Foundation

// Mirrors of the daemon's JSON (internal/mux/http.go, stats.go, exit.go).

struct StatusResponse: Decodable {
    var tailnets: [TailnetStatus]
    var tun: TUNInfo?
    var exitNode: ExitStatus?
    var version: String?
    var update: UpdateInfo?
}

struct UpdateInfo: Decodable {
    var current: String
    var latest: String?
    var available: Bool
    var url: String?
    var state: String
    var error: String?
    var auto: Bool
}

struct TailnetStatus: Decodable, Identifiable {
    var id: String { name }
    var name: String
    var enabled: Bool
    var state: String?
    var authUrl: String?
    var suffix: String?
    var selfIps: [String]?
    var peers: Int
    var online: Int
    var routes: [String]?
    var error: String?

    var running: Bool { enabled && state == "Running" }
}

struct TUNInfo: Decodable {
    var interface: String
    var fakeRange: String
    var dns: Bool
}

struct StatsResponse: Decodable {
    var intervalMs: Int
    var tailnets: [TailnetStats]
}

struct TailnetStats: Decodable {
    var name: String
    var enabled: Bool
    var conns: Int
    var rxTotal: UInt64
    var txTotal: UInt64
    var rx: [Double]?
    var tx: [Double]?
}

struct PeerInfo: Decodable, Identifiable, Hashable {
    var id: String { tailnet + "/" + name }
    var tailnet: String
    var name: String
    var alias: String
    var fqdn: String
    var ips: [String]?
    var online: Bool
    var routes: [String]?
}

/// Where an exit node is. Set for Mullvad nodes; every field may be absent.
struct Location: Decodable, Hashable {
    var country: String?
    var countryCode: String?
    var city: String?
    var cityCode: String?
    var priority: Int?

    /// The country's flag, from its two-letter code.
    var flag: String {
        guard let cc = countryCode?.uppercased(), cc.count == 2 else { return "" }
        return String(String.UnicodeScalarView(cc.unicodeScalars.compactMap { Unicode.Scalar(127397 + $0.value) }))
    }
}

/// The chosen exit node and whether traffic can use it right now.
struct ExitStatus: Decodable {
    var tailnet: String
    var node: String
    var name: String?
    var fqdn: String?
    var online: Bool
    var active: Bool
    var location: Location?
    var error: String?

    var displayName: String { name.flatMap { $0.isEmpty ? nil : $0 } ?? node }
}

struct ExitNodeInfo: Decodable, Identifiable, Hashable {
    var id: String
    var tailnet: String
    var name: String
    var fqdn: String
    var ips: [String]?
    var online: Bool
    var mullvad: Bool?
    var location: Location?
    var selected: Bool?

    var isMullvad: Bool { mullvad ?? false }
    var isSelected: Bool { selected ?? false }
    var priority: Int { location?.priority ?? 0 }
    /// What PUT /exit-node gets as "node": the MagicDNS name, else the ID.
    var spec: String { fqdn.isEmpty ? id : fqdn }
    var key: String { tailnet + "/" + id }
}

struct ExitNodesResponse: Decodable {
    var current: ExitStatus?
    var nodes: [ExitNodeInfo]?
}

struct ConfigView: Decodable {
    var path: String
    var stateDir: String
    var config: SavedConfig
}

struct SavedConfig: Decodable {
    var hostname: String?
    var tailnets: [SavedTailnet]?
    var tun: SavedTUN?
    var updates: SavedUpdates?
}

struct SavedTailnet: Decodable {
    var name: String
    var controlUrl: String?
}

struct SavedTUN: Decodable { var enabled: Bool? }
struct SavedUpdates: Decodable { var check: Bool?; var auto: Bool? }

/// Talks to the daemon's HTTP API on loopback.
struct API {
    var base: URL

    /// Finds the daemon's API address from the same config files the CLI
    /// reads, falling back to the default port.
    static func discover() -> API {
        if let env = ProcessInfo.processInfo.environment["TAILMUX_API"], let url = URL(string: env) {
            return API(base: url)
        }
        var paths: [String] = []
        if let p = ProcessInfo.processInfo.environment["TAILMUX_CONFIG"] { paths.append(p) }
        paths.append(NSHomeDirectory() + "/.config/tailmux/config.json")
        paths.append("/opt/homebrew/etc/tailmux/config.json")
        paths.append("/usr/local/etc/tailmux/config.json")
        for p in paths {
            guard let data = FileManager.default.contents(atPath: p),
                  let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any]
            else { continue }
            var addr = (obj["http"] as? String) ?? "127.0.0.1:1056"
            if addr.hasPrefix(":") || addr.hasPrefix("0.0.0.0:") {
                addr = "127.0.0.1:" + (addr.split(separator: ":").last.map(String.init) ?? "1056")
            }
            if let url = URL(string: "http://" + addr) { return API(base: url) }
        }
        return API(base: URL(string: "http://127.0.0.1:1056")!)
    }

    private static let decoder: JSONDecoder = {
        let d = JSONDecoder()
        d.keyDecodingStrategy = .convertFromSnakeCase
        return d
    }()

    private static let session: URLSession = {
        let c = URLSessionConfiguration.ephemeral
        c.timeoutIntervalForRequest = 3
        c.connectionProxyDictionary = [:] // never route the API through a system proxy
        return URLSession(configuration: c)
    }()

    func get<T: Decodable>(_ path: String, as: T.Type) async throws -> T {
        let (data, resp) = try await API.session.data(from: base.appendingPathComponent(path))
        try check(resp, data)
        return try API.decoder.decode(T.self, from: data)
    }

    func getLogs(_ n: Int) async throws -> [String] {
        var c = URLComponents(url: base.appendingPathComponent("logs"), resolvingAgainstBaseURL: false)!
        c.queryItems = [URLQueryItem(name: "n", value: String(n))]
        let (data, resp) = try await API.session.data(from: c.url!)
        try check(resp, data)
        return try API.decoder.decode([String].self, from: data)
    }

    /// Sends a changing request (always with X-Tailmux) and returns the body.
    @discardableResult
    func send(_ method: String, _ path: String, json: [String: Any]? = nil) async throws -> Data {
        var req = URLRequest(url: base.appendingPathComponent(path))
        req.httpMethod = method
        req.setValue("1", forHTTPHeaderField: "X-Tailmux")
        if let json {
            req.setValue("application/json", forHTTPHeaderField: "Content-Type")
            req.httpBody = try JSONSerialization.data(withJSONObject: json)
        }
        let (data, resp) = try await API.session.data(for: req)
        try check(resp, data)
        return data
    }

    func decode<T: Decodable>(_ data: Data, as: T.Type) throws -> T {
        try API.decoder.decode(T.self, from: data)
    }

    func setEnabled(_ name: String, _ on: Bool) async throws {
        var req = URLRequest(url: base.appendingPathComponent("tailnets/\(name)/\(on ? "enable" : "disable")"))
        req.httpMethod = "POST"
        req.setValue("1", forHTTPHeaderField: "X-Tailmux")
        let (data, resp) = try await API.session.data(for: req)
        try check(resp, data)
    }

    /// Picks an exit node, or turns it off with nil. Applies live.
    @discardableResult
    func setExitNode(tailnet: String?, node: String?) async throws -> ExitStatus? {
        var body: [String: Any] = [:]
        if let tailnet, let node {
            body = ["tailnet": tailnet, "node": node]
        }
        let data = try await send("PUT", "exit-node", json: body)
        return try decode(data, as: ExitNodesResponse.self).current
    }

    func startUpdate() async throws {
        var req = URLRequest(url: base.appendingPathComponent("update"))
        req.httpMethod = "POST"
        req.setValue("1", forHTTPHeaderField: "X-Tailmux")
        let (data, resp) = try await API.session.data(for: req)
        try check(resp, data)
    }

    private func check(_ resp: URLResponse, _ data: Data) throws {
        guard let http = resp as? HTTPURLResponse else { return }
        if !(200..<300).contains(http.statusCode) {
            let msg = String(data: data, encoding: .utf8)?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
            throw NSError(domain: "tailmux", code: http.statusCode, userInfo: [NSLocalizedDescriptionKey: msg])
        }
    }
}
