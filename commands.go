package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var inputKeys = []string{"up", "down", "ok", "back", "start", "busy", "custom", "off", "apps", "settings"}

// readJSONArg reads JSON from a literal string, an @file path, or - for stdin.
func readJSONArg(value string) (any, error) {
	var (
		text []byte
		err  error
	)
	switch {
	case value == "-":
		text, err = io.ReadAll(os.Stdin)
	case strings.HasPrefix(value, "@"):
		text, err = os.ReadFile(value[1:])
	default:
		text = []byte(value)
	}
	if err != nil {
		return nil, fail("%v", err)
	}
	var out any
	if err := json.Unmarshal(text, &out); err != nil {
		return nil, fail("invalid JSON: %v", err)
	}
	return out, nil
}

func readBinary(path string) ([]byte, error) {
	if path == "-" {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return nil, fail("%v", err)
		}
		return data, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fail("%v", err)
	}
	return data, nil
}

// simple wires a no-argument command straight onto one endpoint.
func simple(use, short, method, path string) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.NoArgs,
		RunE: run(func(c *Client, _ *cobra.Command, _ []string) (any, error) {
			return c.do(method, path, opts{})
		}),
	}
}

func group(use, short string, children ...*cobra.Command) *cobra.Command {
	cmd := &cobra.Command{Use: use, Short: short}
	cmd.AddCommand(children...)
	return cmd
}

// --- device -----------------------------------------------------------------

func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:       "status [section]",
		Short:     "device status",
		Args:      cobra.MaximumNArgs(1),
		ValidArgs: []string{"all", "device", "firmware", "power", "system"},
		RunE: run(func(c *Client, _ *cobra.Command, args []string) (any, error) {
			section := "all"
			if len(args) == 1 {
				section = args[0]
			}
			if section == "all" {
				return c.get("/status", opts{})
			}
			if err := choice("section", section, "device", "firmware", "power", "system"); err != nil {
				return nil, err
			}
			return c.get("/status/"+section, opts{})
		}),
	}
}

func nameCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "name [new-name]",
		Short: "get or set the device name",
		Args:  cobra.MaximumNArgs(1),
		RunE: run(func(c *Client, _ *cobra.Command, args []string) (any, error) {
			if len(args) == 1 {
				return c.post("/name", opts{params: map[string]any{"name": args[0]}})
			}
			return c.get("/name", opts{})
		}),
	}
}

func accessCmd() *cobra.Command {
	var mode, key string
	c := &cobra.Command{
		Use:   "access",
		Short: "get or set HTTP access mode",
		Args:  cobra.NoArgs,
		RunE: run(func(cl *Client, _ *cobra.Command, _ []string) (any, error) {
			if mode == "" {
				return cl.get("/access", opts{})
			}
			if err := choice("mode", mode, "disabled", "enabled", "key"); err != nil {
				return nil, err
			}
			params := map[string]any{"mode": mode}
			if key != "" {
				params["key"] = key
			}
			return cl.post("/access", opts{params: params})
		}),
	}
	c.Flags().StringVar(&mode, "mode", "", "disabled|enabled|key")
	c.Flags().StringVar(&key, "key", "", "4-10 digit access key (required with --mode key)")
	return c
}

func logDumpCmd() *cobra.Command {
	var filename string
	c := &cobra.Command{
		Use:   "log-dump",
		Short: "dump device logs to storage",
		Args:  cobra.NoArgs,
		RunE: run(func(cl *Client, _ *cobra.Command, _ []string) (any, error) {
			var params map[string]any
			if filename != "" {
				params = map[string]any{"filename": filename}
			}
			return cl.post("/log_dump", opts{params: params})
		}),
	}
	c.Flags().StringVar(&filename, "filename", "", "destination filename")
	return c
}

func inputCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "input <key>...",
		Short: "emulate button presses (" + strings.Join(inputKeys, ", ") + ")",
		Args:  cobra.MinimumNArgs(1),
		RunE: run(func(c *Client, _ *cobra.Command, args []string) (any, error) {
			var results []any
			for _, key := range args {
				if err := choice("key", key, inputKeys...); err != nil {
					return nil, err
				}
				res, err := c.post("/input", opts{params: map[string]any{"key": key}})
				if err != nil {
					return nil, err
				}
				results = append(results, res)
			}
			if len(results) == 1 {
				return results[0], nil
			}
			return results, nil
		}),
	}
}

// --- audio ------------------------------------------------------------------

func audioCmd() *cobra.Command {
	var stock bool
	var app string
	play := &cobra.Command{
		Use:   "play <path>",
		Short: "play an uploaded or stock sound",
		Args:  cobra.ExactArgs(1),
		RunE: run(func(c *Client, _ *cobra.Command, args []string) (any, error) {
			body := map[string]any{}
			if stock {
				body["stock_path"] = args[0]
			} else {
				body["path"] = args[0]
			}
			if app != "" {
				body["application_name"] = app
			}
			return c.post("/audio/play", opts{json: body})
		}),
	}
	play.Flags().BoolVar(&stock, "stock", false, "path refers to a built-in sound")
	play.Flags().StringVar(&app, "app", "busybar-cli", "application_name")

	volume := &cobra.Command{
		Use:   "volume [value]",
		Short: "get or set volume",
		Args:  cobra.MaximumNArgs(1),
		RunE: run(func(c *Client, _ *cobra.Command, args []string) (any, error) {
			if len(args) == 0 {
				return c.get("/audio/volume", opts{})
			}
			v, err := strconv.ParseFloat(args[0], 64)
			if err != nil {
				return nil, fail("volume must be a number")
			}
			return c.post("/audio/volume", opts{params: map[string]any{"volume": v}})
		}),
	}

	return group("audio", "playback and volume",
		play, simple("stop", "stop playback", "DELETE", "/audio/play"), volume)
}

// --- assets -----------------------------------------------------------------

func assetsCmd() *cobra.Command {
	var uploadApp, name string
	upload := &cobra.Command{
		Use:   "upload <file>",
		Short: "upload an asset file",
		Args:  cobra.ExactArgs(1),
		RunE: run(func(c *Client, _ *cobra.Command, args []string) (any, error) {
			data, err := readBinary(args[0])
			if err != nil {
				return nil, err
			}
			remote := name
			if remote == "" {
				remote = filepath.Base(args[0])
			}
			note("uploading %s (%d bytes) for app %q", remote, len(data), uploadApp)
			note("note: bytes are sent as-is; images must already be sized for the " +
				"target display (72x16 front, 160x80 back)")
			return c.post("/assets/upload", opts{
				params:  map[string]any{"file": remote, "application_name": uploadApp},
				data:    data,
				timeout: 120 * time.Second,
			})
		}),
	}
	upload.Flags().StringVar(&uploadApp, "app", "busybar-cli", "application_name")
	upload.Flags().StringVar(&name, "name", "", "filename on the device (default: local basename)")

	var deleteApp string
	del := &cobra.Command{
		Use:   "delete",
		Short: "delete all assets for an app",
		Args:  cobra.NoArgs,
		RunE: run(func(c *Client, _ *cobra.Command, _ []string) (any, error) {
			return c.delete("/assets/upload", opts{params: map[string]any{"application_name": deleteApp}})
		}),
	}
	del.Flags().StringVar(&deleteApp, "app", "", "application_name")
	_ = del.MarkFlagRequired("app")

	return group("assets", "per-app image and sound assets", upload, del)
}

// --- storage ----------------------------------------------------------------

func storageCmd() *cobra.Command {
	var long bool
	ls := &cobra.Command{
		Use:   "ls [path]",
		Short: "list a directory",
		Args:  cobra.MaximumNArgs(1),
		RunE: run(func(c *Client, _ *cobra.Command, args []string) (any, error) {
			path := "/"
			if len(args) == 1 {
				path = args[0]
			}
			res, err := c.get("/storage/list", opts{params: map[string]any{"path": path}})
			if err != nil {
				return nil, err
			}
			obj, ok := res.(map[string]any)
			if long || !ok {
				return res, nil
			}
			items, ok := obj["list"].([]any)
			if !ok {
				return res, nil
			}
			var lines []string
			for _, raw := range items {
				item, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				kind := "-"
				if item["type"] == "dir" {
					kind = "d"
				}
				size := ""
				if n, ok := item["size"].(float64); ok {
					size = strconv.FormatInt(int64(n), 10)
				}
				lines = append(lines, fmt.Sprintf("%s %10s %v", kind, size, item["name"]))
			}
			if len(lines) == 0 {
				return "(empty)", nil
			}
			return strings.Join(lines, "\n"), nil
		}),
	}
	ls.Flags().BoolVarP(&long, "long", "l", false, "raw JSON instead of a table")

	var out string
	get := &cobra.Command{
		Use:   "get <path>",
		Short: "read a file",
		Args:  cobra.ExactArgs(1),
		RunE: run(func(c *Client, _ *cobra.Command, args []string) (any, error) {
			res, err := c.get("/storage/read", opts{params: map[string]any{"path": args[0]}, raw: true})
			if err != nil {
				return nil, err
			}
			data := res.([]byte)
			if out == "" || out == "-" {
				return data, nil
			}
			if err := os.WriteFile(out, data, 0o644); err != nil {
				return nil, fail("%v", err)
			}
			return fmt.Sprintf("wrote %s (%d bytes)", out, len(data)), nil
		}),
	}
	get.Flags().StringVarP(&out, "out", "o", "", "output file, default stdout")

	put := &cobra.Command{
		Use:   "put <local> [remote]",
		Short: "write a file",
		Args:  cobra.RangeArgs(1, 2),
		RunE: run(func(c *Client, _ *cobra.Command, args []string) (any, error) {
			data, err := readBinary(args[0])
			if err != nil {
				return nil, err
			}
			remote := "/" + filepath.Base(args[0])
			if len(args) == 2 {
				remote = args[1]
			}
			return c.post("/storage/write", opts{
				params:  map[string]any{"path": remote},
				data:    data,
				timeout: 120 * time.Second,
			})
		}),
	}

	oneArg := func(use, short, method, path, param string) *cobra.Command {
		return &cobra.Command{
			Use:   use,
			Short: short,
			Args:  cobra.ExactArgs(1),
			RunE: run(func(c *Client, _ *cobra.Command, args []string) (any, error) {
				return c.do(method, path, opts{params: map[string]any{param: args[0]}})
			}),
		}
	}

	mv := &cobra.Command{
		Use:   "mv <old> <new>",
		Short: "rename a file or directory",
		Args:  cobra.ExactArgs(2),
		RunE: run(func(c *Client, _ *cobra.Command, args []string) (any, error) {
			return c.post("/storage/rename", opts{
				params: map[string]any{"old_path": args[0], "new_path": args[1]}})
		}),
	}

	return group("storage", "device filesystem", ls, get, put,
		oneArg("rm <path>", "remove a file or directory", "DELETE", "/storage/remove", "path"),
		oneArg("mkdir <path>", "create a directory", "POST", "/storage/mkdir", "path"),
		mv,
		simple("status", "storage usage", "GET", "/storage/status"))
}

// --- busy timer -------------------------------------------------------------

func busyCmd() *cobra.Command {
	var snapSet string
	snapshot := &cobra.Command{
		Use:   "snapshot",
		Short: "get or set the timer snapshot",
		Args:  cobra.NoArgs,
		RunE: run(func(c *Client, _ *cobra.Command, _ []string) (any, error) {
			if snapSet == "" {
				return c.get("/busy/snapshot", opts{})
			}
			body, err := readJSONArg(snapSet)
			if err != nil {
				return nil, err
			}
			return c.put("/busy/snapshot", opts{json: body})
		}),
	}
	snapshot.Flags().StringVar(&snapSet, "set", "", "JSON literal, @file.json, or -")

	var profSet string
	profile := &cobra.Command{
		Use:   "profile <busy|custom>",
		Short: "get or set a mode profile",
		Args:  cobra.ExactArgs(1),
		RunE: run(func(c *Client, _ *cobra.Command, args []string) (any, error) {
			if err := choice("slot", args[0], "busy", "custom"); err != nil {
				return nil, err
			}
			if profSet == "" {
				return c.get("/busy/profiles/"+args[0], opts{})
			}
			body, err := readJSONArg(profSet)
			if err != nil {
				return nil, err
			}
			return c.put("/busy/profiles/"+args[0], opts{json: body})
		}),
	}
	profile.Flags().StringVar(&profSet, "set", "", "JSON literal, @file.json, or -")

	return group("busy", "BUSY/CUSTOM timer state and profiles", snapshot, profile)
}

// --- wifi / ble -------------------------------------------------------------

func wifiCmd() *cobra.Command {
	var security, ipConfig string
	connect := &cobra.Command{
		Use:   "connect <ssid> [password]",
		Short: "join a network",
		Args:  cobra.RangeArgs(1, 2),
		RunE: run(func(c *Client, _ *cobra.Command, args []string) (any, error) {
			cfg := map[string]any{"ssid": args[0]}
			if len(args) == 2 {
				cfg["password"] = args[1]
			}
			if security != "" {
				if err := choice("security", security,
					"Open", "WPA", "WPA2", "WEP", "WPA/WPA2", "WPA3", "WPA2/WPA3"); err != nil {
					return nil, err
				}
				cfg["security"] = security
			}
			if ipConfig != "" {
				parsed, err := readJSONArg(ipConfig)
				if err != nil {
					return nil, err
				}
				cfg["ip_config"] = parsed
			}
			return c.post("/wifi/connect", opts{json: cfg})
		}),
	}
	connect.Flags().StringVar(&security, "security", "", "Open|WPA|WPA2|WEP|WPA/WPA2|WPA3|WPA2/WPA3")
	connect.Flags().StringVar(&ipConfig, "ip-config", "", `static IP config JSON, e.g. '{"method":"static",...}'`)

	return group("wifi", "Wi-Fi configuration",
		simple("status", "connection status", "GET", "/wifi/status"),
		simple("scan", "list visible networks", "GET", "/wifi/networks"),
		connect,
		// No /wifi/enable or /wifi/disable in openapi.yaml - busybar.py invents
		// them and they 404.
		simple("disconnect", "leave the current network", "POST", "/wifi/disconnect"))
}

func bleCmd() *cobra.Command {
	return group("ble", "Bluetooth LE",
		simple("status", "BLE status", "GET", "/ble/status"),
		simple("enable", "turn BLE on", "POST", "/ble/enable"),
		simple("disable", "turn BLE off", "POST", "/ble/disable"),
		simple("forget", "drop the current pairing", "DELETE", "/ble/pairing"))
}

// --- time -------------------------------------------------------------------

func timeCmd() *cobra.Command {
	set := &cobra.Command{
		Use:   "set <timestamp>",
		Short: "set the device clock (ISO 8601)",
		Args:  cobra.ExactArgs(1),
		RunE: run(func(c *Client, _ *cobra.Command, args []string) (any, error) {
			return c.post("/time/timestamp", opts{params: map[string]any{"timestamp": args[0]}})
		}),
	}
	timezone := &cobra.Command{
		Use:   "timezone [tz]",
		Short: "get or set the timezone, e.g. Europe/Berlin",
		Args:  cobra.MaximumNArgs(1),
		RunE: run(func(c *Client, _ *cobra.Command, args []string) (any, error) {
			if len(args) == 0 {
				return c.get("/time/timezone", opts{})
			}
			return c.post("/time/timezone", opts{params: map[string]any{"timezone": args[0]}})
		}),
	}
	return group("time", "clock and timezone",
		simple("show", "current device time", "GET", "/time"),
		set, timezone,
		simple("tzlist", "list supported timezones", "GET", "/time/tzlist"))
}

// --- update -----------------------------------------------------------------

func updateCmd() *cobra.Command {
	install := &cobra.Command{
		Use:   "install <version>",
		Short: "install a released version",
		Args:  cobra.ExactArgs(1),
		RunE: run(func(c *Client, _ *cobra.Command, args []string) (any, error) {
			return c.post("/update/install", opts{params: map[string]any{"version": args[0]}})
		}),
	}
	changelog := &cobra.Command{
		Use:   "changelog <version>",
		Short: "changelog for a version",
		Args:  cobra.ExactArgs(1),
		RunE: run(func(c *Client, _ *cobra.Command, args []string) (any, error) {
			return c.get("/update/changelog", opts{params: map[string]any{"version": args[0]}})
		}),
	}

	var enable, disable bool
	var start, end string
	autoupdate := &cobra.Command{
		Use:   "autoupdate",
		Short: "get or set the auto-update window",
		Args:  cobra.NoArgs,
		RunE: run(func(c *Client, _ *cobra.Command, _ []string) (any, error) {
			if enable && disable {
				return nil, fail("--enable and --disable are mutually exclusive")
			}
			if !enable && !disable && start == "" && end == "" {
				return c.get("/update/autoupdate", opts{})
			}
			settings := map[string]any{}
			if enable || disable {
				settings["is_enabled"] = enable
			}
			if start != "" {
				settings["interval_start"] = start
			}
			if end != "" {
				settings["interval_end"] = end
			}
			return c.post("/update/autoupdate", opts{json: settings})
		}),
	}
	autoupdate.Flags().BoolVar(&enable, "enable", false, "enable auto-update")
	autoupdate.Flags().BoolVar(&disable, "disable", false, "disable auto-update")
	autoupdate.Flags().StringVar(&start, "start", "", "window start, HH:MM")
	autoupdate.Flags().StringVar(&end, "end", "", "window end, HH:MM")

	upload := &cobra.Command{
		Use:   "upload <file>",
		Short: "flash a local firmware file",
		Args:  cobra.ExactArgs(1),
		RunE: run(func(c *Client, _ *cobra.Command, args []string) (any, error) {
			data, err := readBinary(args[0])
			if err != nil {
				return nil, err
			}
			note("uploading firmware (%d bytes) - do not unplug the device", len(data))
			return c.post("/update", opts{data: data, timeout: 600 * time.Second})
		}),
	}

	return group("update", "firmware updates",
		simple("status", "update state", "GET", "/update/status"),
		simple("check", "check for a new release", "POST", "/update/check"),
		install, changelog,
		simple("abort", "abort an in-flight download", "POST", "/update/abort_download"),
		autoupdate, upload)
}

// --- account / smart home ---------------------------------------------------

func accountCmd() *cobra.Command {
	// No /account/profile in openapi.yaml - busybar.py invents it, along with a
	// set-profile command. Only status, info and backend are real.
	get := &cobra.Command{
		Use:   "get [status|info|backend]",
		Short: "read account state",
		Args:  cobra.MaximumNArgs(1),
		RunE: run(func(c *Client, _ *cobra.Command, args []string) (any, error) {
			what := "status"
			if len(args) == 1 {
				what = args[0]
			}
			if err := choice("action", what, "status", "info", "backend"); err != nil {
				return nil, err
			}
			return c.get("/account/"+what, opts{})
		}),
	}

	setBackend := &cobra.Command{
		Use:   "set-backend <payload>",
		Short: "point the device at a custom backend (JSON literal, @file.json, or -)",
		Args:  cobra.ExactArgs(1),
		RunE: run(func(c *Client, _ *cobra.Command, args []string) (any, error) {
			body, err := readJSONArg(args[0])
			if err != nil {
				return nil, err
			}
			return c.put("/account/backend", opts{json: body})
		}),
	}

	return group("account", "BUSY account linkage", get,
		simple("link", "link the device", "POST", "/account/link"),
		simple("unlink", "unlink the device", "DELETE", "/account"),
		setBackend)
}

func smartHomeCmd() *cobra.Command {
	var startup string
	setSwitch := &cobra.Command{
		Use:   "set-switch <on|off>",
		Short: "set the exposed switch state",
		Args:  cobra.ExactArgs(1),
		RunE: run(func(c *Client, _ *cobra.Command, args []string) (any, error) {
			if err := choice("state", args[0], "on", "off"); err != nil {
				return nil, err
			}
			params := map[string]any{"state": args[0] == "on"}
			if startup != "" {
				if err := choice("startup", startup, "off", "on", "toggle", "last"); err != nil {
					return nil, err
				}
				params["startup"] = startup
			}
			return c.post("/smart_home/switch", opts{params: params})
		}),
	}
	setSwitch.Flags().StringVar(&startup, "startup", "", "off|on|toggle|last")

	return group("smarthome", "Matter smart home integration",
		simple("pairing", "show pairing info", "GET", "/smart_home/pairing"),
		simple("pair-start", "start pairing", "POST", "/smart_home/pairing"),
		simple("pair-stop", "stop pairing", "DELETE", "/smart_home/pairing"),
		simple("switch", "read the exposed switch state", "GET", "/smart_home/switch"),
		setSwitch)
}

// --- raw / config -----------------------------------------------------------

func rawCmd() *cobra.Command {
	var params []string
	var jsonBody, dataFile string
	var binary bool

	c := &cobra.Command{
		Use:   "raw <method> <path>",
		Short: "call any endpoint directly, e.g. raw GET /status/power",
		Args:  cobra.ExactArgs(2),
		RunE: run(func(cl *Client, _ *cobra.Command, args []string) (any, error) {
			o := opts{raw: binary}
			if len(params) > 0 {
				o.params = map[string]any{}
				for _, item := range params {
					k, v, ok := strings.Cut(item, "=")
					if !ok {
						return nil, fail("--param expects key=value, got %q", item)
					}
					o.params[k] = v
				}
			}
			if jsonBody != "" {
				body, err := readJSONArg(jsonBody)
				if err != nil {
					return nil, err
				}
				o.json = body
			}
			if dataFile != "" {
				data, err := readBinary(dataFile)
				if err != nil {
					return nil, err
				}
				o.data = data
			}
			return cl.do(args[0], args[1], o)
		}),
	}
	c.Flags().StringArrayVar(&params, "param", nil, "query parameter, K=V (repeatable)")
	c.Flags().StringVar(&jsonBody, "json", "", "request body: JSON literal, @file.json, or -")
	c.Flags().StringVar(&dataFile, "data", "", "raw body from a file, or - for stdin")
	c.Flags().BoolVar(&binary, "binary", false, "write the response bytes verbatim")
	return c
}

func configCmd() *cobra.Command {
	var reveal bool
	show := &cobra.Command{
		Use:   "show",
		Short: "print the saved config",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if _, err := os.Stat(configPath()); os.IsNotExist(err) {
				return emit("(no config file at " + configPath() + ")")
			}
			cfg := loadConfig()
			if cfg.Token != "" && !reveal {
				if len(cfg.Token) > 4 {
					cfg.Token = cfg.Token[:4] + "..."
				} else {
					cfg.Token = "***"
				}
			}
			return emit(cfg)
		},
	}
	show.Flags().BoolVar(&reveal, "reveal", false, "print the token in full")

	var addr, token string
	var cloud, local bool
	set := &cobra.Command{
		Use:   "set",
		Short: "save defaults",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if cloud && local {
				return fail("--cloud and --local are mutually exclusive")
			}
			cfg := loadConfig()
			if cmd.Flags().Changed("addr") {
				cfg.Addr = addr
			}
			if cmd.Flags().Changed("token") {
				cfg.Token = token
			}
			if cloud || local {
				cfg.Cloud = cloud
			}
			if err := saveConfig(cfg); err != nil {
				return fail("%v", err)
			}
			return emit("saved " + configPath())
		},
	}
	set.Flags().StringVar(&addr, "addr", "", "device address")
	set.Flags().StringVar(&token, "token", "", "access key or cloud API token")
	set.Flags().BoolVar(&cloud, "cloud", false, "default to the cloud proxy")
	set.Flags().BoolVar(&local, "local", false, "default to the local device")

	path := &cobra.Command{
		Use: "path", Short: "print the config file path", Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error { return emit(configPath()) },
	}
	clear := &cobra.Command{
		Use: "clear", Short: "delete the config file", Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			if err := os.Remove(configPath()); err != nil {
				if os.IsNotExist(err) {
					return emit("nothing to remove")
				}
				return fail("%v", err)
			}
			return emit("removed " + configPath())
		},
	}

	return group("config", "stored defaults", show, set, path, clear)
}
