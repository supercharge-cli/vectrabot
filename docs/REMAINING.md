# Vectrabot: what's left

Updated after the takeover viewer fix. Host `276aa28` is on GitHub. This viewer change is not pushed.

## Done locally, not pushed

- The workspace host starts each bot as `supercharge-e2b` with `SUPERCHARGE_DESKTOP_PROTOCOL=1`, display `:99`, and the desktop sidecar on port 9224. That is the controller the CLI uses to see and drive the VM. A new template and a gateway host pin are required before production sandboxes pick it up.
- Takeover no longer imports noVNC as a module. `frontend/vendor/novnc/rfb-bundle.js` is a classic script (`NovncRFB`). The phone page loads the same file from app assets. The old module import is what showed "Importing a module script failed" on PC and a blank screen on the phone.

- `7297c10` loads xterm from `frontend/vendor/xterm` instead of esm.sh. The remote Inter stylesheet is removed so the desktop content policy does not block it. The font falls back to the system UI stack.
- `e9534ae` keeps a model save from closing the provider process while another setup call is running. The phone and the PC retry `/api/providers/save` and `/api/providers/image/save` once after a 502 or 503.
- Android `testDebugUnitTest`: 989 tests, 0 failures, 0 errors.
- Host `go test ./...` passed.

## Still to do

1. **Gateway pin is `276aa28`.** Redeploy the gateway on Coolify with "Redeploy (without cache)" if that host is not the one running. Build pack stays Dockerfile, base dir `/apps/gateway`. The takeover viewer fix is client-only and does not need a new host SHA. Do not redeploy without approval.
2. **Built desktop app is not verified.** The takeover viewer is a local classic script now. Rebuild the RPM, install it, sign in, take over, and check the canvas, mouse, keyboard, and terminal. The copy already installed still has the module-import error.
3. **Packages exist, the live click-through does not.** RPM: `desktop/src-tauri/target/release/bundle/rpm/Vectrabot-1.0.2-1.x86_64.rpm`. Debug APK: `app/app/build/outputs/apk/debug/app-debug.apk`. Sign in, open a workspace, and check takeover plus the terminal in that RPM before calling the desktop done.
4. **Model save was not reproduced against production.** The race is fixed and covered by `TestProviderSaveWaitsForInFlightCall`. A live save on a real workspace is still the check that it no longer returns 502.
5. **Windows, macOS, and .deb** still come only from `.github/workflows/desktop.yml` on a `v*` tag. Do not create a tag unless a release is wanted.
6. **Naming.** The Kotlin package is still `com.openmausbot.companion`. The owner will rename it.
7. **Security.** The old template `supercharge-full-sandbox` still exposes desktop ports. Production should stay on `vectrabot-workspace`. Host `/config` still echoes a provider key to its owner. The website dashboard chat still expects the old daemon. Rotate keys that were pasted into chat.
8. **Test account** `vectrabot-test+20261126t144301z@example.com` is still on production. Delete any workspace left on it.
