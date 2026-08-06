package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// Stamped at build time: -ldflags "-X main.version=… -X main.updateRepo=owner/repo".
// updateRepo empty disables the update check entirely.
var (
	version    = "dev"
	updateRepo = ""
)

var (
	flagAddr    string
	flagToken   string
	flagCloud   bool
	flagTimeout float64
	flagVerbose bool
	flagCompact bool

	client *Client
)

func main() {
	root := newRoot()
	err := root.Execute()

	printUpdateNotice()

	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		var cerr *cliError
		if errors.As(err, &cerr) {
			os.Exit(2)
		}
		os.Exit(1)
	}
}

func newRoot() *cobra.Command {
	root := &cobra.Command{
		Use:           "busybar",
		Short:         "Command line client for the BUSY Bar HTTP API",
		Long:          "Command line client for the BUSY Bar HTTP API.\n\nTalks to the device over USB/LAN (http://10.0.4.20/api) or through the\nBUSY cloud proxy (https://api.busy.app/busybar) with --cloud.",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRun: func(*cobra.Command, []string) {
			addr, token, cloud := resolveSettings()
			client = NewClient(addr, token, cloud,
				time.Duration(flagTimeout*float64(time.Second)), flagVerbose)
			startUpdateCheck()
		},
	}

	f := root.PersistentFlags()
	f.StringVar(&flagAddr, "addr", "", "device address (default 10.0.4.20 over USB) [BUSY_ADDR]")
	f.StringVar(&flagToken, "token", "", "Wi-Fi access key, or cloud API token with --cloud [BUSY_TOKEN]")
	f.BoolVar(&flagCloud, "cloud", false, "talk to the bar through the cloud proxy [BUSY_CLOUD]")
	f.Float64Var(&flagTimeout, "timeout", 30, "request timeout in seconds")
	f.BoolVarP(&flagVerbose, "verbose", "v", false, "log requests to stderr")
	f.BoolVar(&flagCompact, "compact", false, "single-line JSON output")

	root.AddCommand(
		statusCmd(),
		simple("version", "firmware and API version", "GET", "/version"),
		nameCmd(),
		simple("transport", "active network interface", "GET", "/transport"),
		accessCmd(),
		logDumpCmd(),
		inputCmd(),
		displayCmd(),
		audioCmd(),
		assetsCmd(),
		storageCmd(),
		busyCmd(),
		wifiCmd(),
		bleCmd(),
		timeCmd(),
		updateCmd(),
		accountCmd(),
		smartHomeCmd(),
		rawCmd(),
		configCmd(),
	)
	return root
}

// resolveSettings applies flag > env > config file > default.
func resolveSettings() (addr, token string, cloud bool) {
	cfg := loadConfig()

	addr = firstNonEmpty(flagAddr, os.Getenv("BUSY_ADDR"), cfg.Addr)
	token = firstNonEmpty(flagToken, os.Getenv("BUSY_TOKEN"), cfg.Token)

	switch envVal, isSet := envBool("BUSY_CLOUD"); {
	case flagCloud:
		cloud = true
	case isSet:
		cloud = envVal
	default:
		cloud = cfg.Cloud
	}
	return addr, token, cloud
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// handler is a command body that gets the resolved client.
type handler func(c *Client, cmd *cobra.Command, args []string) (any, error)

func run(h handler) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		res, err := h(client, cmd, args)
		if err != nil {
			return err
		}
		return emit(res)
	}
}

func emit(v any) error {
	switch t := v.(type) {
	case nil:
		return nil
	case []byte:
		_, err := os.Stdout.Write(t)
		return err
	case string:
		if t != "" {
			fmt.Println(t)
		}
		return nil
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if !flagCompact {
		enc.SetIndent("", "  ")
	}
	if err := enc.Encode(v); err != nil {
		return err
	}
	_, err := os.Stdout.Write(buf.Bytes())
	return err
}

// --- background update check ------------------------------------------------

// updateNotice stays nil unless a check actually started, so --help never waits.
var updateNotice chan string

func stampPath() string { return filepath.Join(filepath.Dir(configPath()), "last_update_check") }

// startUpdateCheck fires at most once every 4 hours and never blocks the command.
func startUpdateCheck() {
	if updateRepo == "" || version == "dev" || os.Getenv("BUSYBAR_NO_UPDATE_CHECK") != "" {
		return
	}
	if info, err := os.Stat(stampPath()); err == nil && time.Since(info.ModTime()) < 4*time.Hour {
		return
	}
	updateNotice = make(chan string, 1)
	_ = os.MkdirAll(filepath.Dir(stampPath()), 0o700)
	_ = os.WriteFile(stampPath(), []byte(time.Now().Format(time.RFC3339)), 0o600)

	go func() {
		defer close(updateNotice)
		hc := &http.Client{Timeout: 2 * time.Second}
		resp, err := hc.Get("https://api.github.com/repos/" + updateRepo + "/releases/latest")
		if err != nil {
			return
		}
		defer resp.Body.Close()
		var rel struct {
			TagName string `json:"tag_name"`
		}
		if json.NewDecoder(resp.Body).Decode(&rel) != nil {
			return
		}
		if latest := strings.TrimPrefix(rel.TagName, "v"); latest != "" && latest != version {
			updateNotice <- fmt.Sprintf(
				"busybar %s is available (you have %s): https://github.com/%s/releases/latest",
				latest, version, updateRepo)
		}
	}()
}

func printUpdateNotice() {
	if updateNotice == nil {
		return
	}
	select {
	case msg := <-updateNotice:
		if msg != "" {
			note("%s", msg)
		}
	case <-time.After(2 * time.Second):
	}
}
