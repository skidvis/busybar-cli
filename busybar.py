#!/usr/bin/env python3
"""
busybar - a command line client for the BUSY Bar HTTP API.

Talks to the device directly over USB/LAN (http://10.0.4.20/api/...) or through
the BUSY cloud proxy (https://api.busy.app/busybar/...). Standard library only.

  busybar status
  busybar display text "BUILDING" --color '#ff0044'
  busybar display screenshot back -o back.png --scale 4
  busybar storage ls /
  busybar raw GET /status/power

Config is read from flags, then env (BUSY_ADDR / BUSY_TOKEN / BUSY_CLOUD),
then ~/.config/busybar/config.json.
"""

from __future__ import annotations

import argparse
import base64
import binascii
import json
import os
import struct
import sys
import zlib
from urllib import error as urlerror
from urllib import parse as urlparse
from urllib import request as urlrequest

__version__ = "1.0.0"

DEFAULT_ADDR = "10.0.4.20"
CLOUD_BASE = "https://api.busy.app/busybar"
LOCAL_PREFIX = "/api"
API_VERSION = os.environ.get("BUSY_API_VERSION", "25.0.0")
API_VERSION_HEADER = "X-Busy-Api-Version"

CONFIG_PATH = os.path.join(
    os.environ.get("XDG_CONFIG_HOME") or os.path.expanduser("~/.config"),
    "busybar",
    "config.json",
)

INPUT_KEYS = ["up", "down", "ok", "back", "start", "busy", "custom", "off", "apps", "settings"]
FONTS = ["tiny", "small", "normal", "condensed", "bold", "large", "extra_large", "global"]
ALIGNMENTS = [
    "top_left", "top_mid", "top_right",
    "mid_left", "mid_mid", "mid_right",
    "bottom_left", "bottom_mid", "bottom_right",
]

# index, width, height, pixel format
DISPLAYS = {
    "front": (0, 72, 16, "RGB888"),
    "back": (1, 160, 80, "L4"),
}


# --------------------------------------------------------------------------
# errors / output
# --------------------------------------------------------------------------

class CliError(Exception):
    pass


class ApiError(CliError):
    def __init__(self, status, method, url, body):
        self.status = status
        self.body = body
        super().__init__("HTTP {0} on {1} {2}\n{3}".format(status, method, url, body))


def emit(obj, pretty=True):
    """Print a JSON-ish result to stdout."""
    if obj is None or obj == "":
        return
    if isinstance(obj, (bytes, bytearray)):
        sys.stdout.buffer.write(obj)
        return
    if isinstance(obj, str):
        print(obj)
        return
    print(json.dumps(obj, indent=2 if pretty else None, sort_keys=False, ensure_ascii=False))


def note(msg):
    sys.stderr.write(msg.rstrip("\n") + "\n")


# --------------------------------------------------------------------------
# config
# --------------------------------------------------------------------------

def load_config():
    try:
        with open(CONFIG_PATH, "r") as fh:
            data = json.load(fh)
        return data if isinstance(data, dict) else {}
    except FileNotFoundError:
        return {}
    except (OSError, ValueError) as exc:
        note("warning: ignoring unreadable config {0}: {1}".format(CONFIG_PATH, exc))
        return {}


def save_config(cfg):
    os.makedirs(os.path.dirname(CONFIG_PATH), exist_ok=True)
    with open(CONFIG_PATH, "w") as fh:
        json.dump(cfg, fh, indent=2)
        fh.write("\n")
    try:
        os.chmod(CONFIG_PATH, 0o600)
    except OSError:
        pass


def env_flag(name):
    val = os.environ.get(name)
    if val is None:
        return None
    return val.strip().lower() in ("1", "true", "yes", "on")


# --------------------------------------------------------------------------
# client
# --------------------------------------------------------------------------

def normalize_addr(addr):
    addr = addr.strip().rstrip("/")
    if not addr.startswith(("http://", "https://")):
        addr = "http://" + addr
    return addr


class Client(object):
    def __init__(self, addr=None, token=None, cloud=False, timeout=30.0, verbose=False):
        self.cloud = bool(cloud)
        self.token = token
        self.timeout = timeout
        self.verbose = verbose
        if self.cloud:
            self.base = normalize_addr(addr) if addr else CLOUD_BASE
        else:
            self.base = normalize_addr(addr or DEFAULT_ADDR) + LOCAL_PREFIX

    def _headers(self, extra=None):
        headers = {
            "Accept": "application/json",
            API_VERSION_HEADER: API_VERSION,
            "User-Agent": "busybar-cli/" + __version__,
        }
        if self.token:
            if self.cloud:
                headers["Authorization"] = "Bearer " + self.token
            else:
                headers["X-API-Token"] = self.token
        if extra:
            headers.update(extra)
        return headers

    def request(self, method, path, params=None, json_body=None, data=None,
                headers=None, expect_bytes=False, timeout=None):
        if not path.startswith("/"):
            path = "/" + path
        url = self.base + path
        clean = {}
        for key, value in (params or {}).items():
            if value is None:
                continue
            if isinstance(value, bool):
                value = "true" if value else "false"
            clean[key] = value
        if clean:
            url += "?" + urlparse.urlencode(clean)

        body = data
        extra = dict(headers or {})
        if json_body is not None:
            body = json.dumps(json_body).encode("utf-8")
            extra.setdefault("Content-Type", "application/json")
        elif isinstance(body, (bytes, bytearray)):
            extra.setdefault("Content-Type", "application/octet-stream")

        if self.verbose:
            note("> {0} {1}".format(method, url))
            if json_body is not None:
                note("> " + json.dumps(json_body))
            elif body:
                note("> <{0} bytes>".format(len(body)))

        req = urlrequest.Request(url, data=body, method=method.upper())
        for key, value in self._headers(extra).items():
            req.add_header(key, value)

        try:
            with urlrequest.urlopen(req, timeout=timeout or self.timeout) as resp:
                raw = resp.read()
                ctype = resp.headers.get("Content-Type", "")
        except urlerror.HTTPError as exc:
            detail = ""
            try:
                detail = exc.read().decode("utf-8", "replace")
            except Exception:  # noqa: BLE001 - best effort
                pass
            if exc.code == 403:
                detail += (
                    "\nhint: this bar wants authentication. Pass --token "
                    "(access key over Wi-Fi, API token in cloud mode)."
                )
            raise ApiError(exc.code, method.upper(), url, detail.strip())
        except urlerror.URLError as exc:
            raise CliError("could not reach {0}: {1}".format(url, exc.reason))

        if expect_bytes:
            return raw
        if not raw:
            return None
        if "json" in ctype:
            try:
                return json.loads(raw.decode("utf-8"))
            except ValueError:
                pass
        text = raw.decode("utf-8", "replace")
        try:
            return json.loads(text)
        except ValueError:
            return text

    # convenience wrappers
    def get(self, path, **kw):
        return self.request("GET", path, **kw)

    def post(self, path, **kw):
        return self.request("POST", path, **kw)

    def put(self, path, **kw):
        return self.request("PUT", path, **kw)

    def delete(self, path, **kw):
        return self.request("DELETE", path, **kw)


# --------------------------------------------------------------------------
# helpers
# --------------------------------------------------------------------------

def parse_color(value):
    """Accept '#rrggbb', 'rrggbb' or 'r,g,b'. Returns what the API accepts."""
    if value is None:
        return None
    value = value.strip()
    if "," in value:
        parts = [p.strip() for p in value.split(",")]
        if len(parts) != 3:
            raise CliError("color '{0}' must be r,g,b".format(value))
        try:
            return [int(p) for p in parts]
        except ValueError:
            raise CliError("color components must be integers: " + value)
    if not value.startswith("#"):
        value = "#" + value
    if len(value) != 7:
        raise CliError("color '{0}' must look like #rrggbb".format(value))
    return value


def read_json_arg(value):
    """Read JSON from a literal string, a @file path, or '-' for stdin."""
    if value == "-":
        text = sys.stdin.read()
    elif value.startswith("@"):
        with open(value[1:], "r") as fh:
            text = fh.read()
    else:
        text = value
    try:
        return json.loads(text)
    except ValueError as exc:
        raise CliError("invalid JSON: {0}".format(exc))


def read_binary(path):
    if path == "-":
        return sys.stdin.buffer.read()
    with open(path, "rb") as fh:
        return fh.read()


def add_element_common(parser, default_display="front"):
    parser.add_argument("--app", default="busybar-cli", metavar="NAME",
                        help="application_name the drawing is grouped under")
    parser.add_argument("--id", default=None, help="element id (default: derived)")
    parser.add_argument("-d", "--display", choices=["front", "back"], default=default_display)
    parser.add_argument("-x", type=int, default=0)
    parser.add_argument("-y", type=int, default=0)
    parser.add_argument("--align", choices=ALIGNMENTS)
    parser.add_argument("--timeout-ms", type=int, dest="element_timeout",
                        help="auto-remove the element after N ms")
    parser.add_argument("--display-until", help="ISO timestamp to keep the element until")
    parser.add_argument("--priority", type=int, help="draw priority")
    parser.add_argument("--led-color", help="LED notification color, #rrggbb or r,g,b")
    parser.add_argument("--clear", action="store_true",
                        help="clear this app's elements before drawing")


def element_base(args, default_id):
    element = {
        "id": args.id or default_id,
        "x": args.x,
        "y": args.y,
        "display": args.display,
    }
    if args.align:
        element["align"] = args.align
    if args.element_timeout is not None:
        element["timeout"] = args.element_timeout
    if args.display_until:
        element["display_until"] = args.display_until
    return element


def send_draw(client, args, elements):
    payload = {"application_name": args.app, "elements": elements}
    if args.priority is not None:
        payload["priority"] = args.priority
    if args.led_color:
        payload["led_notification_color"] = parse_color(args.led_color)
    if args.clear:
        client.delete("/display/draw", params={"application_name": args.app})
    return client.post("/display/draw", json_body=payload)


def warn_bounds(elements):
    for element in elements:
        spec = DISPLAYS.get(element.get("display", "front"))
        if not spec:
            continue
        _, width, height, _ = spec
        if element.get("x", 0) >= width or element.get("y", 0) >= height:
            note("warning: element '{0}' starts outside the {1} display "
                 "({2}x{3})".format(element.get("id"), element.get("display"), width, height))


# --------------------------------------------------------------------------
# PNG encoding (for screenshots)
# --------------------------------------------------------------------------

def rgb_to_png(rgb, width, height, scale=1):
    if scale > 1:
        scaled = bytearray()
        row_len = width * 3
        for y in range(height):
            row = rgb[y * row_len:(y + 1) * row_len]
            wide = bytearray()
            for x in range(width):
                wide.extend(row[x * 3:x * 3 + 3] * scale)
            scaled.extend(bytes(wide) * scale)
        rgb = bytes(scaled)
        width *= scale
        height *= scale

    raw = bytearray()
    stride = width * 3
    for y in range(height):
        raw.append(0)  # filter type 0
        raw.extend(rgb[y * stride:(y + 1) * stride])

    def chunk(tag, payload):
        body = tag + payload
        return struct.pack(">I", len(payload)) + body + struct.pack(">I", zlib.crc32(body) & 0xFFFFFFFF)

    header = struct.pack(">IIBBBBB", width, height, 8, 2, 0, 0, 0)
    return (b"\x89PNG\r\n\x1a\n"
            + chunk(b"IHDR", header)
            + chunk(b"IDAT", zlib.compress(bytes(raw), 9))
            + chunk(b"IEND", b""))


def decode_frame(raw, pixel_format, width, height):
    """The /screen body is base64 text wrapping a raw framebuffer."""
    try:
        data = base64.b64decode(raw, validate=True)
    except (binascii.Error, ValueError):
        data = bytes(raw)

    if pixel_format == "RGB888":
        rgb = data
    else:  # L4: two 4-bit grayscale samples per byte, low nibble first
        out = bytearray()
        for byte in data:
            for level in (byte & 0x0F, (byte >> 4) & 0x0F):
                value = level * 17
                out.extend((value, value, value))
        rgb = bytes(out)

    expected = width * height * 3
    if len(rgb) < expected:
        raise CliError("short frame: got {0} bytes, expected {1}".format(len(rgb), expected))
    return rgb[:expected]


# --------------------------------------------------------------------------
# command implementations
# --------------------------------------------------------------------------

def cmd_status(client, args):
    path = "/status" if args.section == "all" else "/status/" + args.section
    return client.get(path)


def cmd_version(client, args):
    return client.get("/version")


def cmd_name(client, args):
    if args.new_name:
        return client.post("/name", params={"name": args.new_name})
    return client.get("/name")


def cmd_transport(client, args):
    return client.get("/transport")


def cmd_access(client, args):
    if args.mode:
        params = {"mode": args.mode}
        if args.key:
            params["key"] = args.key
        return client.post("/access", params=params)
    return client.get("/access")


def cmd_log_dump(client, args):
    params = {"filename": args.filename} if args.filename else None
    return client.post("/log_dump", params=params)


def cmd_input(client, args):
    results = [client.post("/input", params={"key": key}) for key in args.keys]
    return results[0] if len(results) == 1 else results


# --- display ---

def cmd_display_text(client, args):
    element = element_base(args, "text")
    element.update({"type": "text", "text": args.text, "font": args.font})
    if args.color:
        element["color"] = parse_color(args.color)
    if args.width is not None:
        element["width"] = args.width
    if args.scroll_rate is not None:
        element["scroll_rate"] = args.scroll_rate
    if args.scroll_start_delay is not None:
        element["scroll_start_delay"] = args.scroll_start_delay
    if args.scroll_repeat_delay is not None:
        element["scroll_repeat_delay"] = args.scroll_repeat_delay
    warn_bounds([element])
    return send_draw(client, args, [element])


def cmd_display_image(client, args):
    element = element_base(args, "image")
    element["type"] = "animation" if args.animation else "image"
    if args.stock:
        element["stock_path"] = args.path
    else:
        element["path"] = args.path
    if args.opacity is not None:
        element["opacity"] = args.opacity
    if args.animation:
        element["loop"] = args.loop
    warn_bounds([element])
    return send_draw(client, args, [element])


def cmd_display_countdown(client, args):
    element = element_base(args, "countdown")
    element.update({
        "type": "countdown",
        "timestamp": args.timestamp,
        "direction": args.direction,
        "show_hours": args.show_hours,
    })
    if args.color:
        element["color"] = parse_color(args.color)
    warn_bounds([element])
    return send_draw(client, args, [element])


def cmd_display_rect(client, args):
    element = element_base(args, "rect")
    element.update({
        "type": "rectangle",
        "width": args.width,
        "height": args.height,
        "fill": args.fill,
    })
    if args.radius is not None:
        element["radius"] = args.radius
    if args.fill_colors:
        element["fill_colors"] = [parse_color(c) for c in args.fill_colors]
    if args.border_width is not None:
        element["border_width"] = args.border_width
    if args.border_color:
        element["border_color"] = parse_color(args.border_color)
    warn_bounds([element])
    return send_draw(client, args, [element])


def cmd_display_draw(client, args):
    payload = read_json_arg(args.payload)
    if isinstance(payload, list):
        payload = {"elements": payload}
    if not isinstance(payload, dict):
        raise CliError("draw payload must be an object or a list of elements")
    payload.setdefault("application_name", args.app)
    if args.app != "busybar-cli":
        payload["application_name"] = args.app
    warn_bounds(payload.get("elements") or [])
    if args.clear:
        client.delete("/display/draw", params={"application_name": payload["application_name"]})
    return client.post("/display/draw", json_body=payload)


def cmd_display_clear(client, args):
    params = {"application_name": args.app} if args.app else None
    return client.delete("/display/draw", params=params)


def cmd_display_brightness(client, args):
    if args.value is None:
        return client.get("/display/brightness")
    value = args.value
    if value != "auto":
        try:
            value = int(value)
        except ValueError:
            raise CliError("brightness must be 0-100 or 'auto'")
        if not 0 <= value <= 100:
            raise CliError("brightness must be between 0 and 100")
    return client.post("/display/brightness", params={"value": value})


def cmd_display_screenshot(client, args):
    index, width, height, pixel_format = DISPLAYS[args.which]
    raw = client.get("/screen", params={"display": index}, expect_bytes=True)
    rgb = decode_frame(raw, pixel_format, width, height)
    png = rgb_to_png(rgb, width, height, max(1, args.scale))
    if args.out == "-":
        sys.stdout.buffer.write(png)
        return None
    with open(args.out, "wb") as fh:
        fh.write(png)
    return "wrote {0} ({1}x{2}{3})".format(
        args.out, width * args.scale, height * args.scale,
        ", scaled {0}x".format(args.scale) if args.scale > 1 else "")


# --- audio ---

def cmd_audio_play(client, args):
    body = {}
    if args.stock:
        body["stock_path"] = args.path
    else:
        body["path"] = args.path
    if args.app:
        body["application_name"] = args.app
    return client.post("/audio/play", json_body=body)


def cmd_audio_stop(client, args):
    return client.delete("/audio/play")


def cmd_audio_volume(client, args):
    if args.value is None:
        return client.get("/audio/volume")
    return client.post("/audio/volume", params={"volume": args.value})


# --- assets ---

def cmd_assets_upload(client, args):
    data = read_binary(args.file)
    name = args.name or os.path.basename(args.file)
    note("uploading {0} ({1} bytes) for app '{2}'".format(name, len(data), args.app))
    note("note: bytes are sent as-is; images must already be sized for the "
         "target display (72x16 front, 160x80 back)")
    return client.post("/assets/upload", params={"file": name, "application_name": args.app},
                       data=data, timeout=120)


def cmd_assets_delete(client, args):
    return client.delete("/assets/upload", params={"application_name": args.app})


# --- storage ---

def cmd_storage_ls(client, args):
    result = client.get("/storage/list", params={"path": args.path})
    if args.long or not isinstance(result, dict) or "list" not in result:
        return result
    lines = []
    for item in result.get("list") or []:
        kind = "d" if item.get("type") == "dir" else "-"
        size = item.get("size")
        lines.append("{0} {1:>10} {2}".format(kind, "" if size is None else size, item.get("name", "")))
    return "\n".join(lines) if lines else "(empty)"


def cmd_storage_get(client, args):
    data = client.get("/storage/read", params={"path": args.path}, expect_bytes=True)
    if args.out in (None, "-"):
        sys.stdout.buffer.write(data)
        return None
    with open(args.out, "wb") as fh:
        fh.write(data)
    return "wrote {0} ({1} bytes)".format(args.out, len(data))


def cmd_storage_put(client, args):
    data = read_binary(args.local)
    remote = args.remote or "/" + os.path.basename(args.local)
    return client.post("/storage/write", params={"path": remote}, data=data, timeout=120)


def cmd_storage_rm(client, args):
    return client.delete("/storage/remove", params={"path": args.path})


def cmd_storage_mkdir(client, args):
    return client.post("/storage/mkdir", params={"path": args.path})


def cmd_storage_mv(client, args):
    return client.post("/storage/rename", params={"old_path": args.old, "new_path": args.new})


def cmd_storage_status(client, args):
    return client.get("/storage/status")


# --- busy timer ---

def cmd_busy_snapshot(client, args):
    if args.set:
        return client.put("/busy/snapshot", json_body=read_json_arg(args.set))
    return client.get("/busy/snapshot")


def cmd_busy_profile(client, args):
    if args.set:
        return client.put("/busy/profiles/" + args.slot, json_body=read_json_arg(args.set))
    return client.get("/busy/profiles/" + args.slot)


# --- wifi / ble ---

def cmd_wifi_status(client, args):
    return client.get("/wifi/status")


def cmd_wifi_networks(client, args):
    return client.get("/wifi/networks")


def cmd_wifi_connect(client, args):
    config = {"ssid": args.ssid}
    if args.password:
        config["password"] = args.password
    if args.security:
        config["security"] = args.security
    if args.ip_config:
        config["ip_config"] = read_json_arg(args.ip_config)
    return client.post("/wifi/connect", json_body=config)


def cmd_wifi_simple(client, args):
    return client.post("/wifi/" + args.action)


def cmd_ble(client, args):
    if args.action == "status":
        return client.get("/ble/status")
    if args.action == "forget":
        return client.delete("/ble/pairing")
    return client.post("/ble/" + args.action)


# --- time ---

def cmd_time(client, args):
    return client.get("/time")


def cmd_time_set(client, args):
    return client.post("/time/timestamp", params={"timestamp": args.timestamp})


def cmd_time_timezone(client, args):
    if args.timezone:
        return client.post("/time/timezone", params={"timezone": args.timezone})
    return client.get("/time/timezone")


def cmd_time_tzlist(client, args):
    return client.get("/time/tzlist")


# --- update ---

def cmd_update_status(client, args):
    return client.get("/update/status")


def cmd_update_check(client, args):
    return client.post("/update/check")


def cmd_update_install(client, args):
    return client.post("/update/install", params={"version": args.version})


def cmd_update_changelog(client, args):
    return client.get("/update/changelog", params={"version": args.version})


def cmd_update_abort(client, args):
    return client.post("/update/abort_download")


def cmd_update_autoupdate(client, args):
    if args.enable is None and not args.start and not args.end:
        return client.get("/update/autoupdate")
    settings = {}
    if args.enable is not None:
        settings["is_enabled"] = args.enable
    if args.start:
        settings["interval_start"] = args.start
    if args.end:
        settings["interval_end"] = args.end
    return client.post("/update/autoupdate", json_body=settings)


def cmd_update_upload(client, args):
    data = read_binary(args.file)
    note("uploading firmware ({0} bytes) - do not unplug the device".format(len(data)))
    return client.post("/update", data=data, timeout=600)


# --- account ---

def cmd_account(client, args):
    if args.action == "status":
        return client.get("/account/status")
    if args.action == "info":
        return client.get("/account/info")
    if args.action == "link":
        return client.post("/account/link")
    if args.action == "unlink":
        return client.delete("/account")
    if args.action == "backend":
        return client.get("/account/backend")
    return client.get("/account/profile")


def cmd_account_profile_set(client, args):
    params = {"profile": args.profile}
    if args.custom_url:
        params["custom_url"] = args.custom_url
    return client.post("/account/profile", params=params)


def cmd_account_backend_set(client, args):
    return client.put("/account/backend", json_body=read_json_arg(args.payload))


# --- smart home ---

def cmd_smarthome(client, args):
    if args.action == "pairing":
        return client.get("/smart_home/pairing")
    if args.action == "pair-start":
        return client.post("/smart_home/pairing")
    if args.action == "pair-stop":
        return client.delete("/smart_home/pairing")
    return client.get("/smart_home/switch")


def cmd_smarthome_switch_set(client, args):
    params = {"state": args.state == "on"}
    if args.startup:
        params["startup"] = args.startup
    return client.post("/smart_home/switch", params=params)


# --- raw / config ---

def cmd_raw(client, args):
    params = {}
    for item in args.param or []:
        if "=" not in item:
            raise CliError("--param expects key=value, got '{0}'".format(item))
        key, value = item.split("=", 1)
        params[key] = value
    json_body = read_json_arg(args.json_body) if args.json_body else None
    data = read_binary(args.data) if args.data else None
    return client.request(args.method, args.path, params=params or None,
                          json_body=json_body, data=data, expect_bytes=args.binary)


def cmd_config(client, args):
    cfg = load_config()
    if args.config_action == "path":
        return CONFIG_PATH
    if args.config_action == "show":
        shown = dict(cfg)
        if shown.get("token"):
            shown["token"] = shown["token"][:4] + "..." if len(shown["token"]) > 4 else "***"
        return shown or "(no config file at {0})".format(CONFIG_PATH)
    if args.config_action == "clear":
        try:
            os.remove(CONFIG_PATH)
            return "removed " + CONFIG_PATH
        except FileNotFoundError:
            return "nothing to remove"
    for field in ("addr", "token"):
        value = getattr(args, field)
        if value is not None:
            cfg[field] = value
    if args.set_cloud is not None:
        cfg["cloud"] = args.set_cloud
    save_config(cfg)
    return "saved " + CONFIG_PATH


# --------------------------------------------------------------------------
# argument parsing
# --------------------------------------------------------------------------

def build_parser():
    parser = argparse.ArgumentParser(
        prog="busybar",
        description="Command line client for the BUSY Bar HTTP API.",
        epilog="Endpoints follow the device API: https://api.busy.app/busybar/docs",
        formatter_class=argparse.RawDescriptionHelpFormatter,
    )
    parser.add_argument("--addr", help="device address (default 10.0.4.20 over USB)")
    parser.add_argument("--token", help="Wi-Fi access key, or cloud API token with --cloud")
    parser.add_argument("--cloud", action="store_true", default=None,
                        help="talk to the bar through https://api.busy.app/busybar")
    parser.add_argument("--timeout", type=float, default=30.0, help="request timeout in seconds")
    parser.add_argument("--compact", action="store_true", help="single-line JSON output")
    parser.add_argument("-v", "--verbose", action="store_true", help="log requests to stderr")
    parser.add_argument("--version", action="version", version="busybar " + __version__)

    sub = parser.add_subparsers(dest="command", metavar="<command>")
    sub.required = True

    # status / device
    p = sub.add_parser("status", help="device status")
    p.add_argument("section", nargs="?", default="all",
                   choices=["all", "device", "firmware", "power", "system"])
    p.set_defaults(func=cmd_status)

    p = sub.add_parser("version", help="firmware and API version")
    p.set_defaults(func=cmd_version)

    p = sub.add_parser("name", help="get or set the device name")
    p.add_argument("new_name", nargs="?")
    p.set_defaults(func=cmd_name)

    p = sub.add_parser("transport", help="active network interface")
    p.set_defaults(func=cmd_transport)

    p = sub.add_parser("access", help="get or set HTTP access mode")
    p.add_argument("--mode", choices=["disabled", "enabled", "key"])
    p.add_argument("--key", help="4-10 digit access key (required with --mode key)")
    p.set_defaults(func=cmd_access)

    p = sub.add_parser("log-dump", help="dump device logs to storage")
    p.add_argument("--filename")
    p.set_defaults(func=cmd_log_dump)

    p = sub.add_parser("input", help="emulate button presses")
    p.add_argument("keys", nargs="+", choices=INPUT_KEYS)
    p.set_defaults(func=cmd_input)

    # display
    p = sub.add_parser("display", help="draw on the screens")
    dsub = p.add_subparsers(dest="display_command", metavar="<subcommand>")
    dsub.required = True

    sp = dsub.add_parser("text", help="show text")
    sp.add_argument("text")
    sp.add_argument("--font", choices=FONTS, default="small")
    sp.add_argument("--color", help="#rrggbb or r,g,b")
    sp.add_argument("--width", type=int, help="clip width, enables scrolling")
    sp.add_argument("--scroll-rate", type=int)
    sp.add_argument("--scroll-start-delay", type=int)
    sp.add_argument("--scroll-repeat-delay", type=int)
    add_element_common(sp)
    sp.set_defaults(func=cmd_display_text)

    sp = dsub.add_parser("image", help="show an uploaded image or animation")
    sp.add_argument("path", help="asset filename on the device")
    sp.add_argument("--stock", action="store_true", help="path refers to a built-in asset")
    sp.add_argument("--animation", action="store_true", help="draw as an animation element")
    sp.add_argument("--loop", action="store_true", help="loop the animation")
    sp.add_argument("--opacity", type=int)
    add_element_common(sp, default_display="back")
    sp.set_defaults(func=cmd_display_image)

    sp = dsub.add_parser("countdown", help="show a live countdown")
    sp.add_argument("timestamp", help="ISO timestamp to count to/from")
    sp.add_argument("--direction", choices=["time_left", "time_since"], default="time_left")
    sp.add_argument("--show-hours", choices=["when_non_zero", "always"], default="when_non_zero")
    sp.add_argument("--color")
    add_element_common(sp)
    sp.set_defaults(func=cmd_display_countdown)

    sp = dsub.add_parser("rect", help="draw a rectangle")
    sp.add_argument("--width", type=int, required=True)
    sp.add_argument("--height", type=int, required=True)
    sp.add_argument("--radius", type=int)
    sp.add_argument("--fill", choices=["none", "solid", "gradient_h", "gradient_v"], default="solid")
    sp.add_argument("--fill-colors", nargs="+", help="one color for solid, two for a gradient")
    sp.add_argument("--border-width", type=int)
    sp.add_argument("--border-color")
    add_element_common(sp)
    sp.set_defaults(func=cmd_display_rect)

    sp = dsub.add_parser("draw", help="POST a raw display payload")
    sp.add_argument("payload", help="JSON literal, @file.json, or - for stdin")
    sp.add_argument("--app", default="busybar-cli")
    sp.add_argument("--clear", action="store_true")
    sp.set_defaults(func=cmd_display_draw)

    sp = dsub.add_parser("clear", help="clear drawn elements")
    sp.add_argument("--app", default=None, help="limit to one application_name")
    sp.set_defaults(func=cmd_display_clear)

    sp = dsub.add_parser("brightness", help="get or set brightness")
    sp.add_argument("value", nargs="?", help="0-100 or 'auto'")
    sp.set_defaults(func=cmd_display_brightness)

    sp = dsub.add_parser("screenshot", help="capture a display as PNG")
    sp.add_argument("which", nargs="?", choices=["front", "back"], default="front")
    sp.add_argument("-o", "--out", default="screenshot.png", help="output file, or - for stdout")
    sp.add_argument("--scale", type=int, default=1, help="nearest-neighbour upscale factor")
    sp.set_defaults(func=cmd_display_screenshot)

    # audio
    p = sub.add_parser("audio", help="playback and volume")
    asub = p.add_subparsers(dest="audio_command", metavar="<subcommand>")
    asub.required = True

    sp = asub.add_parser("play", help="play an uploaded or stock sound")
    sp.add_argument("path")
    sp.add_argument("--stock", action="store_true")
    sp.add_argument("--app", default="busybar-cli")
    sp.set_defaults(func=cmd_audio_play)

    sp = asub.add_parser("stop", help="stop playback")
    sp.set_defaults(func=cmd_audio_stop)

    sp = asub.add_parser("volume", help="get or set volume")
    sp.add_argument("value", nargs="?", type=float)
    sp.set_defaults(func=cmd_audio_volume)

    # assets
    p = sub.add_parser("assets", help="per-app image and sound assets")
    asub2 = p.add_subparsers(dest="assets_command", metavar="<subcommand>")
    asub2.required = True

    sp = asub2.add_parser("upload", help="upload an asset file")
    sp.add_argument("file")
    sp.add_argument("--app", default="busybar-cli")
    sp.add_argument("--name", help="filename on the device (default: local basename)")
    sp.set_defaults(func=cmd_assets_upload)

    sp = asub2.add_parser("delete", help="delete all assets for an app")
    sp.add_argument("--app", required=True)
    sp.set_defaults(func=cmd_assets_delete)

    # storage
    p = sub.add_parser("storage", help="device filesystem")
    ssub = p.add_subparsers(dest="storage_command", metavar="<subcommand>")
    ssub.required = True

    sp = ssub.add_parser("ls", help="list a directory")
    sp.add_argument("path", nargs="?", default="/")
    sp.add_argument("-l", "--long", action="store_true", help="raw JSON instead of a table")
    sp.set_defaults(func=cmd_storage_ls)

    sp = ssub.add_parser("get", help="read a file")
    sp.add_argument("path")
    sp.add_argument("-o", "--out", help="output file, default stdout")
    sp.set_defaults(func=cmd_storage_get)

    sp = ssub.add_parser("put", help="write a file")
    sp.add_argument("local")
    sp.add_argument("remote", nargs="?")
    sp.set_defaults(func=cmd_storage_put)

    sp = ssub.add_parser("rm", help="remove a file or directory")
    sp.add_argument("path")
    sp.set_defaults(func=cmd_storage_rm)

    sp = ssub.add_parser("mkdir", help="create a directory")
    sp.add_argument("path")
    sp.set_defaults(func=cmd_storage_mkdir)

    sp = ssub.add_parser("mv", help="rename a file or directory")
    sp.add_argument("old")
    sp.add_argument("new")
    sp.set_defaults(func=cmd_storage_mv)

    sp = ssub.add_parser("status", help="storage usage")
    sp.set_defaults(func=cmd_storage_status)

    # busy timer
    p = sub.add_parser("busy", help="BUSY/CUSTOM timer state and profiles")
    bsub = p.add_subparsers(dest="busy_command", metavar="<subcommand>")
    bsub.required = True

    sp = bsub.add_parser("snapshot", help="get or set the timer snapshot")
    sp.add_argument("--set", metavar="JSON", help="JSON literal, @file.json, or -")
    sp.set_defaults(func=cmd_busy_snapshot)

    sp = bsub.add_parser("profile", help="get or set a mode profile")
    sp.add_argument("slot", choices=["busy", "custom"])
    sp.add_argument("--set", metavar="JSON", help="JSON literal, @file.json, or -")
    sp.set_defaults(func=cmd_busy_profile)

    # wifi
    p = sub.add_parser("wifi", help="Wi-Fi configuration")
    wsub = p.add_subparsers(dest="wifi_command", metavar="<subcommand>")
    wsub.required = True

    sp = wsub.add_parser("status", help="connection status")
    sp.set_defaults(func=cmd_wifi_status)

    sp = wsub.add_parser("scan", help="list visible networks")
    sp.set_defaults(func=cmd_wifi_networks)

    sp = wsub.add_parser("connect", help="join a network")
    sp.add_argument("ssid")
    sp.add_argument("password", nargs="?")
    sp.add_argument("--security",
                    choices=["Open", "WPA", "WPA2", "WEP", "WPA/WPA2", "WPA3", "WPA2/WPA3"])
    sp.add_argument("--ip-config", metavar="JSON",
                    help='static IP config, e.g. \'{"method":"static","ip":"..."}\'')
    sp.set_defaults(func=cmd_wifi_connect)

    for action, helptext in [("disconnect", "leave the current network"),
                             ("enable", "turn the radio on"),
                             ("disable", "turn the radio off")]:
        sp = wsub.add_parser(action, help=helptext)
        sp.set_defaults(func=cmd_wifi_simple, action=action)

    # ble
    p = sub.add_parser("ble", help="Bluetooth LE")
    p.add_argument("action", choices=["status", "enable", "disable", "forget"])
    p.set_defaults(func=cmd_ble)

    # time
    p = sub.add_parser("time", help="clock and timezone")
    tsub = p.add_subparsers(dest="time_command", metavar="<subcommand>")
    tsub.required = True

    sp = tsub.add_parser("show", help="current device time")
    sp.set_defaults(func=cmd_time)

    sp = tsub.add_parser("set", help="set the device clock")
    sp.add_argument("timestamp", help="ISO 8601 timestamp")
    sp.set_defaults(func=cmd_time_set)

    sp = tsub.add_parser("timezone", help="get or set the timezone")
    sp.add_argument("timezone", nargs="?", help="e.g. Europe/Berlin")
    sp.set_defaults(func=cmd_time_timezone)

    sp = tsub.add_parser("tzlist", help="list supported timezones")
    sp.set_defaults(func=cmd_time_tzlist)

    # update
    p = sub.add_parser("update", help="firmware updates")
    usub = p.add_subparsers(dest="update_command", metavar="<subcommand>")
    usub.required = True

    sp = usub.add_parser("status", help="update state")
    sp.set_defaults(func=cmd_update_status)

    sp = usub.add_parser("check", help="check for a new release")
    sp.set_defaults(func=cmd_update_check)

    sp = usub.add_parser("install", help="install a released version")
    sp.add_argument("version")
    sp.set_defaults(func=cmd_update_install)

    sp = usub.add_parser("changelog", help="changelog for a version")
    sp.add_argument("version")
    sp.set_defaults(func=cmd_update_changelog)

    sp = usub.add_parser("abort", help="abort an in-flight download")
    sp.set_defaults(func=cmd_update_abort)

    sp = usub.add_parser("autoupdate", help="get or set the auto-update window")
    group = sp.add_mutually_exclusive_group()
    group.add_argument("--enable", dest="enable", action="store_true", default=None)
    group.add_argument("--disable", dest="enable", action="store_false")
    sp.add_argument("--start", help="window start, HH:MM")
    sp.add_argument("--end", help="window end, HH:MM")
    sp.set_defaults(func=cmd_update_autoupdate)

    sp = usub.add_parser("upload", help="flash a local firmware file")
    sp.add_argument("file")
    sp.set_defaults(func=cmd_update_upload)

    # account
    p = sub.add_parser("account", help="BUSY account linkage")
    acsub = p.add_subparsers(dest="account_command", metavar="<subcommand>")
    acsub.required = True

    sp = acsub.add_parser("get", help="read account state")
    sp.add_argument("action", nargs="?", default="status",
                    choices=["status", "info", "profile", "backend"])
    sp.set_defaults(func=cmd_account)

    for action in ("link", "unlink"):
        sp = acsub.add_parser(action, help=action + " the device")
        sp.set_defaults(func=cmd_account, action=action)

    sp = acsub.add_parser("set-profile", help="switch the account profile")
    sp.add_argument("profile")
    sp.add_argument("--custom-url")
    sp.set_defaults(func=cmd_account_profile_set)

    sp = acsub.add_parser("set-backend", help="point the device at a custom backend")
    sp.add_argument("payload", help="JSON literal, @file.json, or -")
    sp.set_defaults(func=cmd_account_backend_set)

    # smart home
    p = sub.add_parser("smarthome", help="Matter smart home integration")
    shsub = p.add_subparsers(dest="smarthome_command", metavar="<subcommand>")
    shsub.required = True

    for action, helptext in [("pairing", "show pairing info"),
                             ("pair-start", "start pairing"),
                             ("pair-stop", "stop pairing"),
                             ("switch", "read the exposed switch state")]:
        sp = shsub.add_parser(action, help=helptext)
        sp.set_defaults(func=cmd_smarthome, action=action)

    sp = shsub.add_parser("set-switch", help="set the exposed switch state")
    sp.add_argument("state", choices=["on", "off"])
    sp.add_argument("--startup", choices=["off", "on", "toggle", "last"])
    sp.set_defaults(func=cmd_smarthome_switch_set)

    # raw
    p = sub.add_parser("raw", help="call any endpoint directly")
    p.add_argument("method", help="GET, POST, PUT, DELETE")
    p.add_argument("path", help="path after /api, e.g. /status/power")
    p.add_argument("--param", action="append", metavar="K=V", help="query parameter")
    p.add_argument("--json", dest="json_body", metavar="JSON",
                   help="request body: JSON literal, @file.json, or -")
    p.add_argument("--data", metavar="FILE", help="raw body from a file, or - for stdin")
    p.add_argument("--binary", action="store_true", help="write the response bytes verbatim")
    p.set_defaults(func=cmd_raw)

    # config
    p = sub.add_parser("config", help="stored defaults")
    csub = p.add_subparsers(dest="config_action", metavar="<subcommand>")
    csub.required = True
    csub.add_parser("show", help="print the saved config").set_defaults(func=cmd_config)
    csub.add_parser("path", help="print the config file path").set_defaults(func=cmd_config)
    csub.add_parser("clear", help="delete the config file").set_defaults(func=cmd_config)
    sp = csub.add_parser("set", help="save defaults")
    sp.add_argument("--addr")
    sp.add_argument("--token")
    group = sp.add_mutually_exclusive_group()
    group.add_argument("--cloud", dest="set_cloud", action="store_true", default=None)
    group.add_argument("--local", dest="set_cloud", action="store_false")
    sp.set_defaults(func=cmd_config)

    return parser


def resolve_settings(args):
    cfg = load_config()
    addr = args.addr or os.environ.get("BUSY_ADDR") or cfg.get("addr")
    token = args.token or os.environ.get("BUSY_TOKEN") or cfg.get("token")
    cloud = args.cloud
    if cloud is None:
        cloud = env_flag("BUSY_CLOUD")
    if cloud is None:
        cloud = bool(cfg.get("cloud"))
    return addr, token, cloud


def main(argv=None):
    parser = build_parser()
    args = parser.parse_args(argv)

    if args.command == "config":
        # config commands never touch the network
        try:
            emit(cmd_config(None, args), pretty=not args.compact)
        except CliError as exc:
            note("error: " + str(exc))
            return 1
        return 0

    addr, token, cloud = resolve_settings(args)
    client = Client(addr=addr, token=token, cloud=cloud,
                    timeout=args.timeout, verbose=args.verbose)

    try:
        emit(args.func(client, args), pretty=not args.compact)
    except ApiError as exc:
        note("error: " + str(exc))
        return 2
    except CliError as exc:
        note("error: " + str(exc))
        return 1
    except KeyboardInterrupt:
        return 130
    except BrokenPipeError:
        return 0
    return 0


if __name__ == "__main__":
    sys.exit(main())
