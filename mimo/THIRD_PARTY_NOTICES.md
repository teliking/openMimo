# Third-party sources and attribution

This repository contains independently authored Go protocol code, extracted research metadata from the locally supplied DJI Mimo APK, and attributed regression fixtures. No project-wide license has been selected for the new code. Third-party licenses below apply to their respective material; they do not grant rights to DJI binaries or code.

## Kaze for DJI

- Project: [brianmerchant/Kaze-for-DJI](https://github.com/brianmerchant/Kaze-for-DJI)
- Inspected revision: `341a35de18493ff61f97c93b8b10161a7512aa36`
- Copyright (c) 2026 Brian Merchant, MIT; full text: [LICENSES/Kaze-MIT.txt](LICENSES/Kaze-MIT.txt).
- Copied fixtures: `protocol/testdata/kaze-crc.json`, `kaze-transport.json`, `kaze-gimbal.json`, `kaze-camera.json`, from the respective `protocol/test-vectors/*/*.json` files. Evidence metadata inside fixtures describes upstream provenance; it is not an openMimo hardware-validation claim.
- The protocol documentation and Android/iOS implementation informed UDP framing, BLE pairing, settings mappings and telemetry. Sources: [Pocket 3 protocol](https://github.com/brianmerchant/Kaze-for-DJI/blob/341a35de18493ff61f97c93b8b10161a7512aa36/docs/POCKET3_DUML_PROTOCOL.md), [camera settings](https://github.com/brianmerchant/Kaze-for-DJI/blob/341a35de18493ff61f97c93b8b10161a7512aa36/docs/CAMERA_SETTINGS_PROTOCOL.md).
- `protocol/testdata/kaze-property.json` transcribes subscription/push byte vectors and expected fields from `ios/Pocket3ControllerTests/Pocket3CameraReadbackTests.swift` at the same revision. Typed property decoders also refer to the upstream `Pocket3CameraReadback` implementations. The new native video setters and conversion tables are separately derived from the supplied APK.

Upstream also includes Osmosis and dji-remote work. Their notices are retained as [LICENSES/Osmosis-MIT.txt](LICENSES/Osmosis-MIT.txt) and [LICENSES/dji-remote-MIT.txt](LICENSES/dji-remote-MIT.txt) (Copyright (c) 2026 Dmytro Antonov). These notices were obtained from Kaze's Android tree.

## Other research references and tools

- Go runtime: BSD 3-Clause, Copyright (c) 2009 The Go Authors; full license retained in [LICENSES/Go-BSD-3-Clause.txt](LICENSES/Go-BSD-3-Clause.txt). The Android APK packages retained license texts under `assets/licenses/`, accessible from the app's Logs page.

- [yigitkonur/lib-osmo-ble](https://github.com/yigitkonur/lib-osmo-ble): inspected for comparison; no fixtures or source copied into the Go implementation. Conflicting claims were not treated as authoritative over byte vectors or APK evidence.
- [xaionaro-go/djictl](https://github.com/xaionaro-go/djictl), inspected revision `ddeced5422fe3a27075602d41b49e61ca60c99d8`: comparison only; no source copied.
- [JADX](https://github.com/skylot/jadx), version 1.5.6: analysis tool, locally under ignored `tools/`.
- IDA/Hex-Rays: locally available analysis tool; native addresses in research notes refer to the reconstructed ELF, not process addresses.

The original APK, decrypted DEX, reconstructed shared libraries, Java reference adapters and local analysis-tool distributions are research inputs/artifacts, not part of a licensed third-party software distribution. Extraction does not establish permission to redistribute DJI material.
