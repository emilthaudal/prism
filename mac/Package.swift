// swift-tools-version: 6.2
import PackageDescription

let package = Package(
    name: "Prism",
    platforms: [.macOS(.v26)],
    products: [
        .executable(name: "Prism", targets: ["Prism"]),
        .library(name: "DeskCore", targets: ["DeskCore"]),
        .executable(name: "prism-motion", targets: ["PrismMotion"]),
    ],
    targets: [
        .target(
            name: "DeskCore",
            swiftSettings: [.swiftLanguageMode(.v6)]
        ),
        .executableTarget(
            name: "Prism",
            dependencies: ["DeskCore", "MotionCheck"],
            swiftSettings: [.swiftLanguageMode(.v6)]
        ),
        // Test tooling: the motion recorder and checker behind make motion.
        .target(
            name: "MotionCheck",
            swiftSettings: [.swiftLanguageMode(.v6)]
        ),
        .executableTarget(
            name: "PrismMotion",
            dependencies: ["MotionCheck"],
            swiftSettings: [.swiftLanguageMode(.v6)]
        ),
        .testTarget(
            name: "DeskCoreTests",
            dependencies: ["DeskCore"],
            swiftSettings: [.swiftLanguageMode(.v6)]
        ),
        .testTarget(
            name: "MotionCheckTests",
            dependencies: ["MotionCheck"],
            swiftSettings: [.swiftLanguageMode(.v6)]
        ),
    ]
)
