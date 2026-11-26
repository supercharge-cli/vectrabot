# Vectrabot: what's left (as of 26 Nov 2026, 22:30 IST)

Work was stopped on request. Everything finished so far is on `main`; this file lists what is unfinished or unverified.

## Pushed in this batch
- `feat(bots)`: main bot + created bots + per-bot icons (host, PC, Android).
  - Host: one built-in main bot (cannot be deleted, returns 409, always sorted first), pinned bots, duplicate bot,
    icon kinds (generated face, emoji, letter, uploaded image with circle/rounded/square crop),
    `PUT/GET/DELETE /api/bots/{id}/avatar`, unique colour per bot, validated profile edits, migration marks the oldest bot as main.
  - PC + Android show the same icon everywhere (sidebar, header, message labels, info pane, routines) and have an icon picker in the bot editor.
  - Verified: host Go tests pass; Android Bots* tests 24/24; live run `bots-v9` 22/22 against the production cloud + gateway
    (create, emoji, image, letter, pin, duplicate, delete, chat with 2 bots, host restart and sandbox pause/resume keep everything).
- `fix(desktop)`: desktop takeover error "Importing a module script failed".
  - Root cause: the VNC client was imported from `esm.sh`, which the desktop app's security policy (`script-src 'self'`) blocks.
  - Fix: VNC client bundled under `frontend/vendor/novnc`, CSP `connect-src` allows `ws:`/`http:` too. Version bumped to 1.0.2.

## Not done / not verified
1. **Gateway still runs the old host.** `apps/gateway/Dockerfile` in cloud-infra pins `VECTRABOT_SHA=b8b7fdc...`.
   The new bot/icon features need the host from this push. Bump `VECTRABOT_SHA` to the latest vectrabot `main` commit,
   push cloud-infra, then redeploy the gateway on Coolify (Redeploy without cache).
2. **Terminal in the desktop app is likely broken the same way takeover was**: `frontend/app.js` still imports
   `@xterm/xterm` and `@xterm/addon-fit` from `esm.sh`. Vendor them under `frontend/vendor/` like noVNC (and the xterm CSS if loaded remotely).
   Also check for any other `https://` `import(...)` or `<script src>` in `frontend/`.
3. **Takeover fix not tested end to end** in the built app (stopped before the Xvfb run). Test: build RPM 1.0.2, attach a workspace, take over desktop, check the canvas and mouse/keyboard.
4. **Not rebuilt**: RPM 1.0.2 and a new debug APK (the last shared builds are RPM 1.0.1 and the APK from `b8b7fdc`).
5. **Full Android unit suite not re-run** after the bot changes (only the Bots* tests, 24/24). Run `./gradlew testDebugUnitTest` in `app/`.
6. **Model save sometimes returns 502** (user report). Not investigated yet. Likely the host or CLI restarting while saving; reproduce and add a retry or keep the host up.
7. **Windows / macOS / .deb builds** never run: `.github/workflows/desktop.yml` builds them on a `v*` tag push. No tag has been made.
8. **Naming cleanup**: Kotlin package is still `com.openmausbot.companion` and ~290 files carry the old name. The owner said they'd do the rename.
9. **Security follow-ups**: old template `supercharge-full-sandbox` exposes desktop ports (production now uses `vectrabot-workspace`);
   host `/config` echoes the raw provider key back to its owner; the dashboard chat on the website still expects the old daemon; rotate the keys that were shared in chat.
10. **Test leftovers**: test account `vectrabot-test+20261126t144301z@example.com` exists on production; any test workspace on it can be deleted.
