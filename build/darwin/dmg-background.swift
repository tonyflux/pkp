import AppKit

// Finder coordinates and text positions use points; the TIFF is rendered at 2×.
let size = NSSize(width: 640, height: 440)
let bitmap = NSBitmapImageRep(
    bitmapDataPlanes: nil, pixelsWide: 1280, pixelsHigh: 880,
    bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true,
    isPlanar: false, colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0
)!
bitmap.size = size
let context = NSGraphicsContext(bitmapImageRep: bitmap)!
NSGraphicsContext.saveGraphicsState()
NSGraphicsContext.current = context

let ink = NSColor(calibratedRed: 0.13, green: 0.16, blue: 0.21, alpha: 1)
let muted = NSColor(calibratedRed: 0.39, green: 0.43, blue: 0.49, alpha: 1)
NSGradient(
    starting: NSColor(calibratedWhite: 1, alpha: 1),
    ending: NSColor(calibratedRed: 0.94, green: 0.96, blue: 0.99, alpha: 1)
)!.draw(in: NSRect(origin: .zero, size: size), angle: -90)

func text(_ value: String, top: CGFloat, fontSize: CGFloat, weight: NSFont.Weight, color: NSColor) {
    let paragraph = NSMutableParagraphStyle()
    paragraph.alignment = .center
    (value as NSString).draw(
        in: NSRect(x: 24, y: size.height - top - 30, width: size.width - 48, height: 30),
        withAttributes: [
            .font: NSFont.systemFont(ofSize: fontSize, weight: weight),
            .foregroundColor: color,
            .paragraphStyle: paragraph,
        ]
    )
}

text("Install PKP", top: 38, fontSize: 28, weight: .semibold, color: ink)
text("Drag PKP to the Applications folder", top: 84, fontSize: 18, weight: .medium, color: ink)

// The two real Finder icons sit at (172, 230) and (468, 230).
let arrow = NSBezierPath()
arrow.move(to: NSPoint(x: 294, y: 210))
arrow.line(to: NSPoint(x: 346, y: 210))
arrow.move(to: NSPoint(x: 334, y: 222))
arrow.line(to: NSPoint(x: 346, y: 210))
arrow.line(to: NSPoint(x: 334, y: 198))
arrow.lineWidth = 3
arrow.lineCapStyle = .round
arrow.lineJoinStyle = .round
muted.withAlphaComponent(0.65).setStroke()
arrow.stroke()

NSColor(calibratedWhite: 0.85, alpha: 1).setFill()
NSRect(x: 48, y: 104, width: 544, height: 0.5).fill()
text("Open PKP from Applications after installation, then eject this disk.", top: 350, fontSize: 13, weight: .medium, color: muted)
text("Blocked on first launch? System Settings → Privacy & Security → Open Anyway", top: 380, fontSize: 12, weight: .regular, color: muted)

NSGraphicsContext.restoreGraphicsState()
let output = URL(fileURLWithPath: CommandLine.arguments[1])
try bitmap.representation(using: .tiff, properties: [:])!.write(to: output)
