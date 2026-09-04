# GoSSH — an SSH client that lives in your browser

An SSH client built on the Go stack: a small local server runs on your
machine and your browser **is** the terminal UI. Manage a host inventory,
keep multiple sessions in tabs, transfer files over SFTP, set up port
forwards, chain through jump hosts, and store credentials in the system
keyring.

```
gossh serve
# HTTP server is listening at: http://127.0.0.1:9049
# Open the page with the access token:
#   http://127.0.0.1:9049/?token=4f0a...
```

Open the printed URL and you are in. The server only listens on
`127.0.0.1` and is guarded by an access token — private keys never leave
the process.

> 简体中文版说明见 [README.zh.md](README.zh.md).

## Features

- **Host inventory**: create/update/delete, groups, search, and `via`
  jump chains of arbitrary depth (ProxyJump semantics)
- **Multi-session tabs**: SSH sessions, SFTP browsing and single-command
  execution results side by side; drag tabs to reorder, order persisted
  per device in localStorage (`gossh.tabOrder`)
- **Credentials**: private key paths, ssh-agent, or passwords; passwords
  and key passphrases are stored encrypted in the system keyring
  (Linux Secret Service / macOS Keychain / Windows Credential Manager),
  falling back to in-memory only when no keyring daemon is available
- **Host keys**: TOFU trust management (`~/.gossh/known_hosts`); a
  changed key refuses the connection
- **Port forwards**: local / remote / dynamic (SOCKS5)
- **Detach-surviving sessions**: closing or refreshing the browser does
  not kill the SSH session; it idles out (default 900s) before teardown
- **One-shot commands**: `gossh run <host> '<cmd>'` without a browser,
  plus an in-browser entry point
- **One binary**: the frontend is embedded via `go:embed`; cross-compiles
  with no Node runtime required

## Installation

```sh
curl -fsSL https://raw.githubusercontent.com/gausszhou/gossh/master/scripts/install.sh | sh
# options: sh install.sh --version v0.0.1 --prefix ~/.local --repo owner/gossh
```

or build from source (Go 1.26+; a frontend build needs Node 18+ / pnpm):

```sh
make build      # frontend + embedded static + ./build/gossh
make release    # 5-platform matrix + sha256sums.txt
```

## Usage

### Serve

```sh
gossh serve                          # 127.0.0.1:9049 by default, prints the token URL
gossh serve --port 0                 # random port
gossh serve --token my-token         # fixed token
gossh serve --timeout 3600           # seconds a detached session survives (0 = never)
gossh serve --ws-origin '^http://127\.0\.0\.1'   # extra WebSocket origin restriction
```

First-run flow:

1. Add hosts with `gossh hosts add` or through the "new host" form in
   the browser;
2. Click "connect" on a host row — enter a password / key passphrase
   when asked, optionally "save to keyring";
3. Work in the session tab; SFTP and port forwards live in the tab
   toolbar.

### CLI

```sh
gossh hosts add --name prod --address 10.0.0.5 --user root --key ~/.ssh/id_ed25519
gossh hosts add --name bastion --address 1.2.3.4 --user ops
gossh hosts list
gossh hosts rm prod
gossh run prod 'uptime && df -h'     # exit codes pass through; works headless
gossh version
```

## Security model

- Listens on `127.0.0.1` only; a random access token gates every
  `/api/*` call and the WebSocket (`Authorization: Bearer` /
  `X-Gossh-Token` / `?token=`), compared in constant time — see
  [ADR 0005](docs/adr/0005-access-token-posture.md)
- Passwords and passphrases go to the system keyring only, never to disk
  in plain text; nothing is persisted when the keyring is unavailable
- TOFU host-key verification applies to every hop, including jump
  hosts; a fingerprint mismatch refuses the connection
- Data never leaves the local process; if you expose the server to the
  network, terminate TLS via a reverse proxy and use `--ws-origin`

## Architecture

```
internal/api        HTTP/WS routing, token, hosts/SFTP/forwards/run handlers
internal/session    session registry and lifecycle (idempotent create,
                    preemption, idle expiry — ported from gotty)
internal/terminal   browser binary frame protocol ("webtty", ported from gotty)
internal/sshtty     the session.Terminal implementation over SSH (remote PTY shell)
internal/sshx       chain dialing (arbitrary-depth jump), credential
                    resolution, TOFU trust store, keyring
internal/host       host inventory (hosts.json) and connection-chain resolution
apps/web            Vue3 + Vite + xterm.js (tabs / inventory / SFTP)
```

See `docs/adr/` (0001–0005) and `CONTEXT.md` (domain glossary).

## Development

```sh
make install   # pnpm install
make build     # frontend + static + ./build/gossh
make test      # go vet + gofmt + go test (including core tests ported from gotty)
make release   # linux/amd64+arm64, darwin/amd64+arm64, windows/amd64
scripts/smoke.sh   # end-to-end smoke against a local sshd
```

## License

MIT. The base code is ported from
[gotty](https://github.com/gausszhou/gotty) (original author Iwasaki
Yudai); attributions are kept in LICENSE.