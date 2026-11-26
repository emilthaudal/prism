import AppKit
import Foundation

let options: LaunchOptions
do {
    options = try LaunchOptions(arguments: Array(ProcessInfo.processInfo.arguments.dropFirst()))
} catch {
    FileHandle.standardError.write(Data("prism: \(error)\n\(LaunchOptions.usage)\n".utf8))
    exit(2)
}

let app = NSApplication.shared
let delegate = AppDelegate(options: options)
app.delegate = delegate
app.setActivationPolicy(options.background ? .accessory : .regular)
app.run()
