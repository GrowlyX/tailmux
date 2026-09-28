import AppKit

/// The menu bar icon: Tailscale's 3×3 dot grid, with one dot lit per
/// connected tailnet. Dots light in the order that draws Tailscale's "T"
/// (top row, then down the middle), so five tailnets spell the logo.
enum DotsIcon {
    static let fillOrder = [0, 1, 2, 4, 7, 3, 5, 6, 8]

    static func image(lit: Int, reachable: Bool, size: CGFloat = 18) -> NSImage {
        let img = NSImage(size: NSSize(width: size, height: size), flipped: true) { _ in
            let d = size * 0.235
            let gap = size * 0.1
            let origin = (size - (3 * d + 2 * gap)) / 2
            let litSet = Set(fillOrder.prefix(max(0, min(lit, 9))))
            for i in 0..<9 {
                let row = CGFloat(i / 3), col = CGFloat(i % 3)
                let rect = NSRect(x: origin + col * (d + gap), y: origin + row * (d + gap), width: d, height: d)
                let alpha: CGFloat = !reachable ? 0.18 : (litSet.contains(i) ? 1.0 : 0.3)
                NSColor.black.withAlphaComponent(alpha).setFill()
                NSBezierPath(ovalIn: rect).fill()
            }
            return true
        }
        // Template: macOS tints it for light/dark menu bars.
        img.isTemplate = true
        return img
    }
}
