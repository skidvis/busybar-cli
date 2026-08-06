# busybar

A single-binary CLI for the [BUSY Bar](https://docs.busy.app/bar/dev) HTTP API.
Go port of `busybar.py` — same endpoint coverage, one static executable.

```
curl -fsSL https://raw.githubusercontent.com/skidvis/busybar/main/install.sh | sh
```

or `go install github.com/skidvis/busybar@latest`, or grab a release archive
(macOS Intel/Apple Silicon, Linux x86_64/ARM64, Windows x86_64).

## Quick start

```sh
busybar status
busybar display text "BUILDING" --color '#ff0044' --font bold --for 30s
busybar display screenshot back -o back.png --scale 4
busybar storage ls /
busybar raw GET /status/power
```

## Connecting

Three transports, picked by flag, env, or saved config:

| Mode | Base URL | Auth |
|---|---|---|
| USB (default) | `http://10.0.4.20/api` | none |
| Wi-Fi | `http://<addr>/api` | `--token <access key>` |
| Cloud | `https://api.busy.app/busybar` | `--cloud --token <API token>` |

Precedence is **flag > env > config file > default**.

```sh
busybar --addr 192.168.1.50 --token 1234 status
BUSY_ADDR=192.168.1.50 BUSY_TOKEN=1234 busybar status
busybar config set --addr 192.168.1.50 --token 1234    # ~/.config/busybar/config.json, mode 0600
```

Env vars: `BUSY_ADDR`, `BUSY_TOKEN`, `BUSY_CLOUD`, `BUSY_API_VERSION`,
`BUSYBAR_NO_UPDATE_CHECK`.

## Commands

`status` `version` `name` `transport` `access` `log-dump` `input`
`display` `audio` `assets` `storage` `busy` `wifi` `ble` `time` `update`
`account` `smarthome` `raw` `config`

`busybar <command> --help` for the flags. Anything not wrapped is reachable with
`busybar raw <METHOD> <PATH> [--param k=v] [--json @file] [--data @file]`.

### Display

`display text|image|countdown|rect` build a `DisplayElements` payload and POST it
to `/display/draw`. They share `--app --id -d/--display -x -y --align
--for --display-until --priority --led-color --clear`.

Panels are **72x16 (front)** and **160x80 (back)**; elements starting outside
those bounds get a stderr warning rather than silently not drawing.

`--for` auto-removes the element: `--for 10s`, `--for 2m`. The wire field
(`timeout`) counts **whole seconds**, so anything under 1s is rejected.

Colors accept `#rrggbb`, `#rrggbbaa`, bare hex, `r,g,b` or `r,g,b,a`, and are
normalized to the `#RRGGBBAA` string the device wants (alpha defaults to `FF`).

`--align` sets **which point of the element** is the anchor; `-x/-y` then place
that anchor on the panel. It does *not* centre anything by itself — `--align
center -x 0 -y 0` pins the element's middle to the top-left corner. When you pass
`--align` without coordinates the CLI defaults `-x/-y` to the matching point on
the panel, so `--align center` centres and `--align bottom_right` sits in the
corner. Values: `top_left top_mid top_right mid_left center mid_right
bottom_left bottom_mid bottom_right`.

Text is printable ASCII only (the fonts are bitmap ASCII); `--display-until`
takes a Unix timestamp in seconds and excludes `--for`.

### Screenshots

`GET /screen` advertises `image/bmp` but returns base64 text wrapping a raw
framebuffer: RGB888 on the front, 4-bit greyscale on the back with **the low
nibble holding the left pixel**. `display screenshot` decodes that and writes a
real PNG; use `--scale` because a 72x16 image is otherwise unviewable.

## Building

```sh
go build -o busybar .
go test ./...
```

No cgo, one dependency (cobra). Releases are cut with GoReleaser on a tag.

## The spec

`openapi.yaml` in this repo is pulled straight off a device — every bar serves
its own spec at `http://10.0.4.20/openapi.yaml` (Swagger UI at `/docs/`). It is
the authority; refresh it and re-check when firmware changes:

```sh
curl -o openapi.yaml http://10.0.4.20/openapi.yaml
```

## Known gaps

- **No WebSocket support.** `GET /api/status/ws` upgrades to a WebSocket for
  live state and screen streaming (send `{"enable": true}` after connecting).
  Only the HTTP API is covered here.
- **Asset uploads are byte-exact.** `assets upload` sends the file as-is — images
  must already be sized for the target panel. No client-side conversion.
- **Untested against real hardware.** Verified against a mock server, so URLs,
  headers, and request bodies are correct; the device's actual responses are not
  verified.
