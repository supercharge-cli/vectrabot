# Vectrabot

One repo for the new product.

- `app/` is the new app. It is the Maus Kotlin phone app, with Vectra Cloud sign-in. The Flutter app in cloud-infra is only a feature reference. It is not this app.
- `backend/` is the new Go server that runs inside the E2B sandbox. Files, shell, Supercharge turns, preview, desktop status, agents, and settings live here.
- `frontend/` is the screen for this computer: the same sign-in, workspace, terminal, preview, desktop, and agent settings the phone calls.

Sign-in and payment stay on `https://infra.xibeai.in`. The workspace does not run on the person's own computer.
