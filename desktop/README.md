# Vectrabot desktop

A small Tauri v2 window around the PC client in `frontend/`. It is a cloud
client only: it signs in to `https://infra.xibeai.in` and talks to the
workspace host in each sandbox. This machine is never used as a host.

```sh
cd desktop
npm install
npx tauri build --bundles rpm   # or deb, dmg, nsis
```

GitHub Actions builds Windows, macOS, and Linux packages when a `v*` tag is
pushed. Creating that tag or a GitHub Release is a separate step.
