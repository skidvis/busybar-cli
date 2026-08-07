# busybar

A single-binary CLI for the [BUSY Bar](https://docs.busy.app/bar/dev) HTTP API.
Go port of `busybar.py` — same endpoint coverage, one static executable.

```
curl -fsSL https://raw.githubusercontent.com/skidvis/busybar-cli/main/install.sh | sh
```

or `go install github.com/skidvis/busybar-cli@latest`, or grab a release archive
(macOS Intel/Apple Silicon, Linux x86_64/ARM64, Windows x86_64).

On Windows use the `.zip` — the Linux build is an ELF binary and Git Bash / MSYS2
can only exec Windows executables. WSL2 runs the Linux build fine, but its NAT
usually can't reach the bar's USB address, so from WSL you want Wi-Fi or cloud
mode (or `networkingMode=mirrored` in `.wslconfig`).

## Quick start

Plug the bar in over USB and it answers on `10.0.4.20` with no auth:

```sh
busybar status                                    # everything the device knows
busybar display text "BUILDING" --color '#ff0044' --font bold --for 30s
busybar display screenshot front -o front.png --scale 6
busybar storage ls
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

## Examples

Output below is real, from a bar on firmware `api_semver 25.0.0`.

> **PowerShell users:** PowerShell strips inner double quotes when it calls a
> native `.exe`, so inline JSON arguments arrive mangled. Every flag that takes
> JSON also accepts `@file.json` or `-` for stdin — use those instead:
> `busybar busy profile busy --set "@profile.json"`.

### Device

```sh
busybar status                 # all sections
busybar status power           # {"battery_charge":99,"state":"discharging",...}
busybar version                # {"api_semver":"25.0.0"}
busybar name                   # {"name":"BUSY Bar"}
busybar name "Desk Bar"        # rename it
busybar transport              # {"type":"usb"} - how you're currently connected
busybar access                 # {"key_valid":false,"mode":"disabled"}
busybar access --mode key --key 4821    # require an access key over Wi-Fi
busybar input ok               # press a button remotely
busybar input down down ok     # several, in order
busybar log-dump               # dump device logs to storage
```

### Display

```sh
# centred text for 30 seconds
busybar display text "ninja" --align center --for 30s

# red, bold, top-right corner of the front panel
busybar display text "LIVE" --color '#ff0044' --font bold --align top_right

# explicit coordinates instead of an anchor
busybar display text "hi" -x 4 -y 2

# scrolling: --width clips the label, scroll_rate is pixels per MINUTE
busybar display text "a long headline that will not fit" \
  --width 72 --scroll-rate 600 --scroll-start-delay 1000

# semi-transparent stock image on the back panel
busybar display image shared/logo.png --stock -d back --opacity 60

# looping animation from your app's assets
busybar display image spinner.gif --animation --loop -d back

# count down to a Unix timestamp (seconds, as a string on the wire)
busybar display countdown 1786060800 --direction time_left --show-hours always

# a full-width red bar with a blinking LED to match
busybar display rect --width 72 --height 16 --fill solid \
  --fill-colors '#ff0000' --led-color '#ff0044ff' --for 10s

# horizontal gradient: repeat --fill-colors, don't comma-join them
busybar display rect --width 72 --height 16 --fill gradient_h \
  --fill-colors '#ff0000' --fill-colors '0,255,0'

# hand-written payload, and clearing up after yourself
busybar display draw @payload.json --clear
busybar display clear --app busybar-cli
busybar display brightness 40      # or 'auto'; omit the value to read it
```

Screenshots decode the raw framebuffer into a real PNG:

```sh
busybar display screenshot front -o front.png --scale 6   # 432x96
busybar display screenshot back  -o back.png  --scale 4   # 640x320
busybar display screenshot front -o - | magick - -resize 400% preview.png
```

### Storage and assets

The device filesystem is rooted at **`/ext`** — `/` is rejected with a 400.

```sh
busybar storage ls                       # defaults to /ext
busybar storage ls /ext/user_assets
busybar storage ls /ext -l                # raw JSON instead of the table
busybar storage status                   # {"free_bytes":7465435136,...}
busybar storage get /ext/Manifest -o Manifest
busybar storage put ./logo.png /ext/user_assets/logo.png
busybar storage mkdir /ext/user_assets/mine
busybar storage mv /ext/a.png /ext/b.png
busybar storage rm /ext/b.png

# per-app assets: bytes are sent as-is, so size images for the panel first
busybar assets upload ./logo.png --app my_app --name logo.png
busybar assets delete --app my_app
```

### Audio

```sh
busybar audio play chime.wav --app my_app
busybar audio play shared/beep.wav --stock
busybar audio stop
busybar audio volume            # {"volume":100}
busybar audio volume 40
```

### Timer, network, time

```sh
busybar busy snapshot                       # current BUSY/CUSTOM timer state
busybar busy profile busy                   # read the BUSY slot
busybar busy profile custom --set @profile.json

busybar wifi status                         # ssid, rssi, ip_config, ...
busybar wifi scan
busybar wifi connect "my-ssid" "hunter2" --security WPA2
busybar wifi connect "my-ssid" --ip-config '{"ip_method":"dhcp"}'
busybar wifi disconnect

busybar ble status                          # {"status":"connectable"}
busybar ble enable
busybar ble disable
busybar ble forget                          # drop the current pairing

busybar time show                           # {"timestamp":"2026-08-06T17:52:01-05:00"}
busybar time set 2026-08-06T17:52:01-05:00  # ISO 8601, timezone qualifier required
busybar time timezone                       # {"abbr":"CDT","name":"Chicago",...}
busybar time timezone America/Chicago
busybar time tzlist
```

### Updates, account, smart home

```sh
busybar update status
busybar update check
busybar update changelog 25.1.0
busybar update install 25.1.0
busybar update abort
busybar update autoupdate --enable --start 03:00 --end 05:00
busybar update upload ./firmware.bin        # flashes; do not unplug

busybar account get status                  # {"status":"connected"}
busybar account get info
busybar account link
busybar account set-backend @backend.json

busybar smarthome pairing
busybar smarthome pair-start
busybar smarthome switch                    # {"state":false}
busybar smarthome set-switch on --startup last
```

### Escape hatch and config

Anything not wrapped is one `raw` call away:

```sh
busybar raw GET /status/power
busybar raw GET /storage/list --param path=/ext
busybar raw POST /display/draw --json @payload.json
busybar raw POST /storage/write --param path=/ext/x.bin --data ./x.bin
busybar raw GET /screen --param display=1 --binary > raw.b64
```

```sh
busybar config set --addr 192.168.1.50 --token 1234
busybar config show                         # token redacted
busybar config show --reveal
busybar config path
busybar config clear
```

### Scripting

`--compact` gives one-line JSON; `-v` logs the request to stderr.

```sh
# battery percentage into a shell variable
pct=$(busybar status power --compact | jq .battery_charge)

# mirror a build status onto the bar
if make -s test; then
  busybar display text "PASS" --color '#00ff00' --align center --for 10s
else
  busybar display text "FAIL" --color '#ff0000' --align center --for 10s
fi

# see exactly what goes over the wire
busybar -v display text "debug" --align center
```

Exit codes: `0` success, `1` API error (non-2xx from the bar), `2` usage or
local error.

## Building

```sh
go build -o busybar .
go test ./...
```

No cgo, one dependency (cobra).

Releases are cut by GoReleaser from `.github/workflows/release.yml`, triggered by
pushing a version tag:

```sh
git tag v0.1.0 && git push origin v0.1.0
```

Tests run first, then every platform archive plus `checksums.txt` is attached to
the GitHub release and the changelog is generated from commits.

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
- **Partially exercised against hardware.** The read paths, `display draw` and
  `display screenshot` are verified on a real bar (firmware `25.0.0`). The
  destructive ones — `update upload`, `account unlink`, `storage rm`,
  `wifi connect` — are only verified against a mock server, so their URLs,
  headers and bodies are right but their responses are not.
