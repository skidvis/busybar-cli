# Build `busybar` — a Go CLI for the BUSY Bar HTTP API

**Audience:** an LLM coding agent. You are building a single, statically-linked,
cross-compiled CLI binary that talks to a BUSY Bar over USB/Wi-Fi/cloud.

**The single source of truth is `openapi.yaml` in this repo.** Every BUSY Bar
serves its own spec — `http://10.0.4.20/openapi.yaml`, with Swagger UI at
`/docs/`. Pull a fresh copy from the target device and work from it:

```
curl -o openapi.yaml http://10.0.4.20/openapi.yaml
```

`busybar.py` is a working Python implementation of the same API and is useful for
command-tree shape, but it was written before the spec was available and it is
wrong in at least five places — element `timeout` units, the `mid_mid` alignment
value, color encoding, and two invented endpoints (§6.2, §5). **Never take a
field name, unit, or enum from `busybar.py` without checking the spec.**

Reference docs: <https://docs.busy.app/bar/dev>

---

## 0. Why Go, and what you get over the Python version

Nothing about the API needs Go. The reason to port is **distribution**:

| | Python file | Go binary |
|---|---|---|
| Install | clone + symlink + hope `python3` exists | `curl … \| sh`, one file |
| Targets | wherever CPython is | darwin/amd64+arm64, linux/amd64+arm64, windows/amd64 |
| Update | `git pull` | self-check + `busybar update-cli` |
| Startup | ~40ms interpreter | ~2ms |

That's the entire delta. Do not "improve" the API layer while porting — it
already matches the device.

---

## 1. Dependencies

Climb the ladder. Use in this order:

1. **stdlib** — `net/http`, `encoding/json`, `encoding/base64`, `image/png`,
   `os`, `flag`. This covers ~95% of the program. `image/png` in particular
   deletes the hand-rolled PNG encoder in `busybar.py` (`rgb_to_png`, ~30 lines
   of zlib + CRC chunk packing) down to `png.Encode(w, img)`.
2. **`github.com/spf13/cobra`** — the one dependency worth taking. The command
   tree is three levels deep (`busybar display screenshot back`) with ~60 leaf
   commands; `flag` alone makes that miserable. Cobra also gives you completions
   and `--help` for free.

**Do not add:** viper (config is ~40 lines of `encoding/json` — see §4),
an HTTP client wrapper (`net/http` is fine), a color library, a table renderer,
or a logging framework. `fmt.Fprintln(os.Stderr, …)` is the logger.

`go.mod` should have exactly one `require` line plus cobra's transitive
`spf13/pflag`.

---

## 2. Repo layout

Few files. Resist `internal/pkg/service/adapter/`.

```
busybar/
  go.mod
  main.go            // cobra root, global flags, settings resolution, error exit
  client.go          // transport, auth, request/response, ApiError
  config.go          // load/save ~/.config/busybar/config.json
  display.go         // display + screen commands, DisplayElements builders
  frame.go           // framebuffer decode + PNG encode
  commands.go        // everything else: status, storage, audio, wifi, time, …
  frame_test.go      // the one required test (see §9)
  .goreleaser.yaml
  install.sh
  README.md
```

If a file passes ~600 lines, split by resource, not by layer.

---

## 3. Transports and auth — get this right first

Three transports, selected by flags/env/config. This is the part a mechanical
docs→CLI generator gets wrong, so it is spec'd exactly:

| Mode | Base URL | Auth header |
|---|---|---|
| USB (default) | `http://10.0.4.20/api` | none |
| Wi-Fi | `http://<addr>/api` | `X-API-Token: <access key>` |
| Cloud | `https://api.busy.app/busybar` | `Authorization: Bearer <token>` |

Rules:

- `--addr` without scheme gets `http://` prepended, trailing `/` stripped, then
  `/api` appended. Cloud mode does **not** append `/api`.
- Every request sends `X-Busy-Api-Version: 25.0.0` (override via
  `BUSY_API_VERSION`), `Accept: application/json`, and
  `User-Agent: busybar-cli/<version>`.
- On HTTP 403, append this hint to the error:
  `hint: this bar wants authentication. Pass --token (access key over Wi-Fi, API token in cloud mode).`
- Response handling: if `--raw-bytes`/`expectBytes` is set, return the body
  untouched. Otherwise try JSON regardless of `Content-Type` (the device lies
  about content types), and fall back to returning the body as a string.

Global flags on the root command:

```
--addr HOST        BUSY_ADDR      device address (default 10.0.4.20)
--token TOKEN      BUSY_TOKEN     access key / API token
--cloud            BUSY_CLOUD     use the cloud proxy
--timeout SECONDS                 default 30
-v, --verbose                     log method, URL, body size to stderr
--json                            force JSON output (no pretty tables)
```

Precedence: **flag > env > config file > built-in default.** Implement this once
in a `resolveSettings()` function, not per-command.

---

## 4. Config

`~/.config/busybar/config.json` (honour `XDG_CONFIG_HOME`; on Windows use
`os.UserConfigDir()`). Chmod `0600` on write — it holds a token.

```json
{ "addr": "10.0.4.20", "token": "…", "cloud": false }
```

Commands: `busybar config show`, `config set <key> <value>`, `config path`.
Redact `token` in `config show` unless `--reveal`. That's the whole feature —
no config schema, no migrations.

---

## 5. The endpoint surface

All paths are relative to the base URL from §3. This is the complete set the
Python client uses; `busybar raw <METHOD> <PATH>` must remain as the escape
hatch for anything not listed.

**Device**
```
GET  /status            /status/<section>      status [section]
GET  /version                                  version
GET  /name              POST /name?name=       name [new-name]
GET  /transport                                transport
GET  /access            POST /access?…         access [--enable/--disable --key]
POST /log_dump                                 log-dump
POST /input?key=<k>                            input <key>…
```
Valid input keys: `up down ok back start busy custom off apps settings`.

**Display**
```
POST   /display/draw          (JSON DisplayElements)   display text|image|countdown|rect|draw
DELETE /display/draw?application_name=                 display clear
GET    /display/brightness    POST /display/brightness?value=   display brightness [0-100|auto]
GET    /screen?display=<0|1>                           display screenshot front|back
```

**Audio**
```
POST   /audio/play (JSON {path|stock_path, application_name})   audio play
DELETE /audio/play                                              audio stop
GET    /audio/volume   POST /audio/volume?volume=               audio volume [n]
```

**Assets**
```
POST   /assets/upload?file=<name>&application_name=  (raw bytes)  assets upload
DELETE /assets/upload?application_name=                          assets delete
```

**Storage**
```
GET    /storage/list?path=          storage ls
GET    /storage/read?path=          storage get      (bytes)
POST   /storage/write?path=         storage put      (raw bytes, 120s timeout)
DELETE /storage/remove?path=        storage rm
POST   /storage/mkdir?path=         storage mkdir
POST   /storage/rename?old_path=&new_path=   storage mv
GET    /storage/status              storage status
```

**Busy timer**
```
GET/PUT /busy/snapshot              busy snapshot [--set JSON]
GET/PUT /busy/profiles/<slot>       busy profile <slot> [--set JSON]
```

**Wi-Fi / BLE** — note there is no `/wifi/enable` or `/wifi/disable`; `busybar.py`
invents both and they 404.
```
GET  /wifi/status /wifi/networks    wifi status | wifi scan
POST /wifi/connect  (JSON)          wifi connect --ssid --password
POST /wifi/disconnect               wifi disconnect
GET  /ble/status                    ble status
POST /ble/<action>                  ble enable|disable|…
DELETE /ble/pairing                 ble unpair
```

**Time**
```
GET  /time                          time
POST /time/timestamp?timestamp=     time set <unix>
GET/POST /time/timezone?timezone=   time timezone [tz]
GET  /time/tzlist                   time tzlist
```

**Update**
```
GET  /update/status                 update status
POST /update/check                  update check
POST /update/install?version=       update install
GET  /update/changelog?version=     update changelog
POST /update/abort_download         update abort
GET/POST /update/autoupdate (JSON)  update autoupdate [--enable/--disable …]
POST /update  (raw firmware bytes, 600s timeout)   update upload <file>
```

**Account / smart home** — there is no `/account/profile`; `busybar.py` invents
it plus a `set-profile` command.
```
GET /account/status /account/info /account/backend
POST /account/link   PUT /account/backend (JSON)
DELETE /account
GET/POST/DELETE /smart_home/pairing
GET/POST /smart_home/switch
```

**Raw**
```
busybar raw <METHOD> <PATH> [--param k=v]… [--data @file|-] [--json @file|-]
```

---

## 6. The non-obvious parts (this is where the value is)

A generator that reads only the docs produces a broken CLI at exactly these
four points. Port them deliberately.

### 6.1 Screenshots are not images

`GET /screen` advertises `Content-Type: image/bmp` but returns **base64 text
wrapping a raw framebuffer**. Naïvely piping it to a file gives you garbage.

```
front  display=0   72 x 16   RGB888  (3 bytes/pixel)
back   display=1  160 x 80   L4      (two 4-bit greyscale samples per byte)
```

**L4 packing: the low nibble is the LEFT pixel.** This is documented nowhere
except a source comment in `busylib` and a community project's bugfix. Expand
each 4-bit level `n` to 8-bit with `n * 17` (so `0xF` → `255`).

```go
// L4: two samples per byte, low nibble first
for _, b := range data {
    for _, lvl := range [2]byte{b & 0x0F, b >> 4} {
        v := lvl * 17
        rgb = append(rgb, v, v, v)
    }
}
```

Validate length (`w*h*3`) and error with `short frame: got N, expected M` rather
than writing a truncated PNG.

Then build an `*image.RGBA` and `png.Encode` it. Support `--scale N` (nearest
neighbour) — a 72×16 PNG is unviewable otherwise — and `-o -` for stdout.

### 6.2 Display elements

`display text|image|countdown|rect` are sugar over one `POST /display/draw`
with a `DisplayElements` payload:

```json
{ "application_name": "busybar-cli",
  "priority": 0,
  "led_notification_color": "#ff0044",
  "elements": [ { "id": "text", "type": "text", "x": 0, "y": 0,
                  "display": "front", "text": "BUILDING", "font": "normal" } ] }
```

Shared element flags (implement once, attach to all four subcommands):
`--app --id -d/--display -x -y --align --for --display-until --priority
--led-color --clear`. `--clear` issues `DELETE /display/draw?application_name=`
first.

- `--font`: `tiny small normal condensed bold large extra_large global`
- `--align` is **an anchor selector, not a centering switch.** The spec says
  "Anchor point of element. Also use `x` and `y` to position element", and `x` is
  "X coordinate of selected anchor point relative to top-left of display". So
  `align: center` with the default `x:0,y:0` pins the element's middle to the
  top-left corner — which looks exactly like alignment being ignored. Make
  `--align` without explicit `-x/-y` default the coordinates to the matching
  point on the panel (`center` → `w/2, h/2`; `bottom_right` → `w, h`). The
  spec's own example is `align: top_mid, x: 36` on the 72-wide front panel.
  Values: `top_left top_mid top_right mid_left center mid_right bottom_left
  bottom_mid bottom_right` — the middle cell is `center`, not `busybar.py`'s
  `mid_mid`.
- **The element `timeout` field is in SECONDS**: "Time in seconds the element
  should be displayed (0 for no timeout). Mutually exclusive with
  display_until." `busybar.py` exposes it as `--timeout-ms` and passes the
  number straight through, so `--timeout-ms 10000` asks the bar to hold the
  element for 10000 *seconds* and it appears never to clear. Take a duration
  flag (`--for 10s`) and convert to whole seconds.
- `display_until` is a **Unix timestamp in seconds**, not ISO 8601, and is
  mutually exclusive with `timeout`. Same for `CountdownElement.timestamp`
  (`^[0-9]+$`) — and it must be sent as a *string*, not a number.
- Colors are **`#RRGGBBAA`** strings, `^#[a-fA-F0-9]{8}$`, always 8 hex digits.
  Accept `#rrggbb`, `#rrggbbaa`, bare hex, `r,g,b`, `r,g,b,a`; default alpha to
  `FF`. `busybar.py` emits bare `#rrggbb` and, for the `r,g,b` form, a JSON
  `[r,g,b]` array — the API takes neither.
- `TextElement.text` is `^[\x20-\x7E]+$` — printable ASCII only, because the
  fonts are bitmap ASCII. Validate client-side for a better error than a 400.
- Other patterns worth enforcing: element `id` is `^[a-zA-Z0-9._-]+$`,
  `stock_path` is `shared/[a-z0-9_.]+$`, `scroll_rate` is pixels per *minute*
  while the scroll delays are milliseconds.
- `text` also takes `--width --scroll-rate --scroll-start-delay --scroll-repeat-delay`
- `image` takes `--stock` (sends `stock_path` instead of `path`), `--opacity`,
  `--animation`, `--loop`
- `rect` takes `--width --height --fill --radius --fill-colors --border-width --border-color`
- `draw` takes a literal JSON string, `@file`, or `-` for stdin; a bare array is
  wrapped as `{"elements": […]}`.

### 6.3 Bounds warnings

Before POSTing, warn on stderr (do not block) if any element's `x >= width` or
`y >= height` for its display. Silent no-op draws are the #1 confusion with this
device.

### 6.4 Asset uploads are byte-exact

`assets upload` sends the file as-is. Images must **already** be sized for the
target panel (72×16 / 160×80). Print that as a stderr note. Do not attempt
client-side conversion — that's a known gap, not a bug.

---

## 7. Output

Two modes, one flag:

- default: pretty JSON (2-space indent, `SetEscapeHTML(false)`) for objects;
  plain text for strings; raw bytes straight to `os.Stdout` for binary.
  `--compact` collapses it to one line.
- `storage ls` gets a small human table (`d`/`-`, size, name) unless `-l/--long`.
  This is the only special-cased renderer. Do not build a generic table engine.

Errors go to stderr and exit non-zero: `2` for usage/CLI errors, `1` for API
errors. Print `HTTP <status> on <METHOD> <url>` plus the response body.

---

## 8. Build, distribute, update

### Cross-compile

```
GOOS=darwin  GOARCH=arm64 go build -trimpath -ldflags "-s -w -X main.version=$VER"
GOOS=darwin  GOARCH=amd64 …
GOOS=linux   GOARCH=amd64 …
GOOS=linux   GOARCH=arm64 …
GOOS=windows GOARCH=amd64 …
```

Pure stdlib + cobra means `CGO_ENABLED=0` works everywhere — keep it that way,
it's what makes the binaries static.

### GoReleaser

A minimal `.goreleaser.yaml` (builds matrix above, `archives` as `.tar.gz` /
`.zip`, `checksum`, `release`) driven by a tag-triggered GitHub Action. Add a
`brews:` block only if a Homebrew tap already exists — otherwise skip it.

### install.sh

~30 lines: detect `uname -s`/`uname -m` → map to the release asset name, `curl
-fsSL` the tarball, extract to `$HOME/.local/bin` (or `/usr/local/bin` if
writable), `chmod +x`. Serve it so `curl -fsSL <url>/install.sh | sh` works.

### Update check

`main.version` is stamped at build time. On startup, at most once every 4 hours
(timestamp in a `last_update_check` file next to the config), fire a background
`GET` to the GitHub releases API with a 2s timeout; if a newer tag exists, print
one line to **stderr** after the command completes. Never block, never
auto-install, honour `BUSYBAR_NO_UPDATE_CHECK=1`.

---

## 9. Required check

One test file. The framebuffer decoder is the only logic that is both non-trivial
and silently wrong when broken:

```go
func TestDecodeL4(t *testing.T) {
    // 0xF0 -> left pixel = low nibble = 0x0 (black), right = 0xF (white)
    got := decodeFrame([]byte{0xF0}, "L4")
    want := []byte{0,0,0, 255,255,255}
    ...
}
func TestDecodeRGB888(t *testing.T) { /* passthrough + short-frame error */ }
```

Plus one round-trip: decode a synthetic front frame → `png.Encode` → `png.Decode`
→ compare pixels. No HTTP mocking framework; if you want a transport test, use
`httptest.NewServer` and assert the URL, headers, and body of one `display text`
call.

---

## 10. Known gaps — carry them forward, don't silently fix

1. **No WebSocket support.** The device exposes live state and screen streaming
   over WS. Go *does* have `nhooyr.io/websocket` / `gorilla/websocket`, unlike
   Python's stdlib — but adding it is a second project. Document it as missing;
   implement only if explicitly asked.
2. **No client-side asset conversion** (see §6.4).
3. **Untested against real hardware.** The reference implementation was verified
   against a mock server: URLs, headers, and bodies are correct; device responses
   are not verified. Keep that caveat in the README.

---

## 11. Acceptance

The port is done when:

- `busybar --help` shows the tree in §5 and every leaf command runs.
- `busybar display screenshot back -o b.png --scale 4` writes a 640×320 PNG that
  opens in an image viewer.
- `busybar raw GET /status/power` works, so nothing in the API is unreachable.
- `busybar config set token …` round-trips and the file is `0600`.
- `go test ./...` passes and `go build` produces one binary with no runtime deps.
- `go vet ./...` is clean.

Do not add: a plugin system, a TUI, shell-completion generators beyond cobra's
built-in `completion` command, telemetry, or a `--dry-run` flag nobody asked for.
