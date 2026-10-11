// PhantomPrompt Lite: the Mac App Store edition. A menu bar app that runs the bundled engine
// ("phantomprompt-engine mas") inside the App Sandbox. The engine is a Claude Code gateway on
// 127.0.0.1:47820 plus the local panel; it starts and stops with this app.
import AppKit
import ServiceManagement
import SwiftUI

@main
struct PhantomPromptLiteApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var delegate

    var body: some Scene {
        MenuBarExtra("PhantomPrompt", systemImage: "theatermasks") {
            MenuContent(engine: delegate.engine)
        }
    }
}

final class AppDelegate: NSObject, NSApplicationDelegate {
    let engine = Engine()

    func applicationDidFinishLaunching(_ notification: Notification) {
        engine.start(openPanel: !CommandLine.arguments.contains("--no-panel"))
    }

    func applicationWillTerminate(_ notification: Notification) {
        engine.stop()
    }
}

struct MenuContent: View {
    @ObservedObject var engine: Engine

    var body: some View {
        Text(engine.status)
        Divider()
        Button("Open Panel") { engine.openPanel() }
            .disabled(!engine.running)
        Button("Copy Coding Agent Setup") { engine.copySetup() }
            .disabled(engine.setup.isEmpty)
        Toggle("Open at Login", isOn: $engine.opensAtLogin)
        Divider()
        Button("Quit PhantomPrompt") { NSApp.terminate(nil) }
            .keyboardShortcut("q")
    }
}

/// Runs the engine as a child process. The engine inherits this app's sandbox, reads commands on
/// stdin ("panel" prints a one-time panel link) and stops when stdin closes, so it never outlives
/// the app.
final class Engine: ObservableObject {
    @Published var status = "Starting…"
    @Published var running = false
    @Published var setup = ""
    @Published var opensAtLogin = SMAppService.mainApp.status == .enabled {
        didSet {
            guard opensAtLogin != (SMAppService.mainApp.status == .enabled) else { return }
            if opensAtLogin {
                try? SMAppService.mainApp.register()
            } else {
                try? SMAppService.mainApp.unregister()
            }
        }
    }

    private let process = Process()
    private let input = Pipe()
    private var buffer = ""
    private var openFirstPanel = true
    private var log: FileHandle?

    func start(openPanel: Bool) {
        openFirstPanel = openPanel
        guard let engine = Bundle.main.url(forAuxiliaryExecutable: "phantomprompt-engine") else {
            status = "Engine missing from the app bundle"
            return
        }
        log = Engine.openLog()
        let output = Pipe()
        process.executableURL = engine
        process.arguments = ["mas"]
        process.standardInput = input
        process.standardOutput = output
        process.standardError = output
        output.fileHandleForReading.readabilityHandler = { [weak self] handle in
            let data = handle.availableData
            guard !data.isEmpty else { return }
            DispatchQueue.main.async { self?.receive(data) }
        }
        process.terminationHandler = { [weak self] process in
            DispatchQueue.main.async {
                self?.running = false
                self?.status = process.terminationStatus == 0 ? "Stopped" : "Stopped with an error. Details: ~/Library/Containers/…/Logs/PhantomPrompt.log"
                if process.terminationStatus == 0 {
                    NSApp.terminate(nil)
                }
            }
        }
        do {
            try process.run()
        } catch {
            status = "Could not start: \(error.localizedDescription)"
        }
    }

    func stop() {
        guard process.isRunning else { return }
        try? input.fileHandleForWriting.close()
        let deadline = Date().addingTimeInterval(3)
        while process.isRunning && Date() < deadline {
            Thread.sleep(forTimeInterval: 0.05)
        }
        if process.isRunning {
            process.terminate()
        }
    }

    func openPanel() {
        input.fileHandleForWriting.write(Data("panel\n".utf8))
    }

    func copySetup() {
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(setup, forType: .string)
    }

    private func receive(_ data: Data) {
        buffer += String(decoding: data, as: UTF8.self)
        while let newline = buffer.firstIndex(of: "\n") {
            let line = String(buffer[..<newline])
            buffer.removeSubrange(...newline)
            if !Engine.carriesCredential(line) {
                log?.write(Data((line + "\n").utf8))
            }
            handle(line)
        }
    }

    // The gateway key and one-time panel links stay out of the log file.
    private static func carriesCredential(_ line: String) -> Bool {
        line.hasPrefix("claude-setup: ") || line.hasPrefix("panel-link: ") || line.contains("one-time link: ")
    }

    private func handle(_ line: String) {
        if line.hasPrefix("claude-setup: ") {
            setup = line.dropFirst("claude-setup: ".count).replacingOccurrences(of: "; ", with: "\n")
            running = true
            status = "Protection on · coding agent gateway 127.0.0.1:47820"
        } else if line.hasPrefix("panel-link: "), let url = URL(string: String(line.dropFirst("panel-link: ".count))) {
            NSWorkspace.shared.open(url)
        } else if line.hasPrefix("Open workspace: "), openFirstPanel, let link = line.components(separatedBy: "one-time link: ").last {
            openFirstPanel = false
            if let url = URL(string: String(link.dropLast())) {
                NSWorkspace.shared.open(url)
            }
        }
    }

    private static func openLog() -> FileHandle? {
        guard let library = FileManager.default.urls(for: .libraryDirectory, in: .userDomainMask).first else { return nil }
        let logs = library.appendingPathComponent("Logs", isDirectory: true)
        try? FileManager.default.createDirectory(at: logs, withIntermediateDirectories: true)
        let file = logs.appendingPathComponent("PhantomPrompt.log")
        if !FileManager.default.fileExists(atPath: file.path) {
            FileManager.default.createFile(atPath: file.path, contents: nil, attributes: [.posixPermissions: 0o600])
        }
        let handle = try? FileHandle(forWritingTo: file)
        handle?.seekToEndOfFile()
        return handle
    }
}
