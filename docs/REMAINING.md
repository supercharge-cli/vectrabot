# Vectrabot: what's left

Updated after the local terminal and provider-save fixes. Nothing from this pass is pushed.

## Done locally, not pushed

- `7297c10` loads xterm from `frontend/vendor/xterm` instead of esm.sh. The remote Inter stylesheet is removed so the desktop content policy does not block it. The font falls back to the system UI stack.
- `e9534ae` keeps a model save from closing the provider process while another setup call is running. The phone and the PC retry `/api/providers/save` and `/api/providers/image/save` once after a 502 or 503.
- Android `testDebugUnitTest`: 989 tests, 0 failures, 0 errors.
- Host `go test ./...` passed.

## Still to do

1. **Push, then point the gateway at this host.** `apps/gateway/Dockerfile` still has `VECTRABOT_SHA=b8b7fdc7b7afa7e00daf76f711cc7b8471b8fe91`. The Docker build fetches that commit from GitHub, so the SHA can move to `e9534ae` only after vectrabot `main` is pushed. Then redeploy the gateway on Coolify with "Redeploy (without cache)". Build pack stays Dockerfile, base dir `/apps/gateway`. Do not push or redeploy without approval.
2. **Built desktop app is not verified.** Takeover (`b7e8f95`) and the new terminal have not been clicked through in an RPM. Build with `cd desktop && npm i && npx tauri build --bundles rpm`, sign in, open a workspace, take over, and check the canvas, mouse, keyboard, and terminal.
3. **Packages exist, the live click-through does not.** RPM: `desktop/src-tauri/target/release/bundle/rpm/Vectrabot-1.0.2-1.x86_64.rpm`. Debug APK: `app/app/build/outputs/apk/debug/app-debug.apk`. Sign in, open a workspace, and check takeover plus the terminal in that RPM before calling the desktop done.
4. **Model save was not reproduced against production.** The race is fixed and covered by `TestProviderSaveWaitsForInFlightCall`. A live save on a real workspace is still the check that it no longer returns 502.
5. **Windows, macOS, and .deb** still come only from `.github/workflows/desktop.yml` on a `v*` tag. Do not create a tag unless a release is wanted.
6. **Naming.** The Kotlin package is still `com.openmausbot.companion`. The owner will rename it.
7. **Security.** The old template `supercharge-full-sandbox` still exposes desktop ports. Production should stay on `vectrabot-workspace`. Host `/config` still echoes a provider key to its owner. The website dashboard chat still expects the old daemon. Rotate keys that were pasted into chat.
8. **Test account** `vectrabot-test+20261126t144301z@example.com` is still on production. Delete any workspace left on it.
