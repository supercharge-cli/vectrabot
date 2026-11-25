# Vectrabot

The phone app and this PC app talk to the same cloud account. The workspace itself runs in an E2B sandbox.

- `backend/` is the Go process inside that sandbox. It serves files, a shell, Supercharge turns, preview status, agents, and settings. It has no website.
- `frontend/` is the shared screen: sign in, pick a workspace, then use the terminal, preview, desktop, and agent settings.
- `pc/` opens that frontend on this computer.

```sh
cd pc && go run .
```

Sign-in uses `https://infra.xibeai.in`. Payment stays on that website.
