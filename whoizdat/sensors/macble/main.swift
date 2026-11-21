// whoizdat-ble: a macOS Bluetooth LE sensor for who_iz_dat.
//
// It scans with the built-in Bluetooth (CoreBluetooth) and writes one JSON
// observation per advertisement to stdout, in the whoizdat obs format.
// It only listens. It never connects to a device.
//
// macOS does not give the real Bluetooth address. "mac" is "uuid:" plus the
// id that this Mac gives the device. The id changes when the device changes
// its random address. So who_iz_dat matches only name, company and service
// rules for these radios, not address rules.
//
// Build (Xcode command line tools):
//   swiftc -O -o whoizdat-ble main.swift \
//     -Xlinker -sectcreate -Xlinker __TEXT -Xlinker __info_plist -Xlinker Info.plist
// Run:
//   ./whoizdat-ble | ../../whoizdat serve -catalog sigs.rsi -
//
// The first run shows a Bluetooth permission prompt for this program or for
// the terminal app.

import CoreBluetooth
import Foundation

setvbuf(stdout, nil, _IOLBF, 0) // one line at a time into the pipe
signal(SIGPIPE, SIG_DFL)          // stop when the reader stops

let iso: ISO8601DateFormatter = {
    let f = ISO8601DateFormatter()
    f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
    return f
}()

func hex(_ d: Data) -> String { d.map { String(format: "%02X", $0) }.joined() }

func log(_ s: String) { FileHandle.standardError.write(("whoizdat-ble: " + s + "\n").data(using: .utf8)!) }

final class Scanner: NSObject, CBCentralManagerDelegate {
    var central: CBCentralManager!

    override init() {
        super.init()
        central = CBCentralManager(delegate: self, queue: nil)
    }

    func centralManagerDidUpdateState(_ c: CBCentralManager) {
        switch c.state {
        case .poweredOn:
            log("scanning")
            // Duplicates on: each advertisement gives a new signal level.
            c.scanForPeripherals(withServices: nil, options: [CBCentralManagerScanOptionAllowDuplicatesKey: true])
        case .unauthorized:
            log("no Bluetooth permission. Allow it in System Settings, Privacy & Security, Bluetooth.")
            exit(1)
        case .unsupported:
            log("this Mac has no Bluetooth LE")
            exit(1)
        case .poweredOff:
            log("Bluetooth is off; waiting")
        default:
            break
        }
    }

    func centralManager(_ c: CBCentralManager, didDiscover p: CBPeripheral,
                        advertisementData ad: [String: Any], rssi: NSNumber) {
        var o: [String: Any] = [
            "at": iso.string(from: Date()),
            "sensor": "macble",
            "kind": "ble",
            "frame": "adv",
            "mac": "uuid:" + p.identifier.uuidString,
        ]
        let level = rssi.intValue
        if level != 127 && level != 0 { o["rssi"] = level } // 127 = not available
        if let name = (ad[CBAdvertisementDataLocalNameKey] as? String) ?? p.name, !name.isEmpty {
            o["name"] = name
        }
        var uuids: [String] = []
        for key in [CBAdvertisementDataServiceUUIDsKey, CBAdvertisementDataOverflowServiceUUIDsKey,
                    CBAdvertisementDataSolicitedServiceUUIDsKey] {
            for u in (ad[key] as? [CBUUID]) ?? [] where !uuids.contains(u.uuidString) {
                uuids.append(u.uuidString)
            }
        }
        if !uuids.isEmpty { o["uuids"] = uuids }
        if let m = ad[CBAdvertisementDataManufacturerDataKey] as? Data, m.count >= 2 {
            let company = Int(m[m.startIndex]) | Int(m[m.startIndex + 1]) << 8 // little endian
            o["mfg"] = [["company": company, "data": hex(m.dropFirst(2))]]
        }
        if let sd = ad[CBAdvertisementDataServiceDataKey] as? [CBUUID: Data], !sd.isEmpty {
            o["svc_data"] = sd.map { ["uuid": $0.key.uuidString, "data": hex($0.value)] }
        }
        guard let json = try? JSONSerialization.data(withJSONObject: o),
              let line = String(data: json, encoding: .utf8) else { return }
        print(line)
    }
}

let scanner = Scanner()
RunLoop.main.run()
