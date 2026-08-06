package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultAddr   = "10.0.4.20"
	cloudBase     = "https://api.busy.app/busybar"
	localPrefix   = "/api"
	apiVersionHdr = "X-Busy-Api-Version"
	defaultAPIVer = "25.0.0"
)

// APIError is a non-2xx response from the bar.
type APIError struct {
	Status int
	Method string
	URL    string
	Body   string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("HTTP %d on %s %s\n%s", e.Status, e.Method, e.URL, e.Body)
}

// cliError is anything wrong on our side: bad flags, unreadable files, bad JSON.
type cliError struct{ msg string }

func (e *cliError) Error() string { return e.msg }

func fail(format string, a ...any) error { return &cliError{fmt.Sprintf(format, a...)} }

func note(format string, a ...any) {
	fmt.Fprintln(os.Stderr, strings.TrimRight(fmt.Sprintf(format, a...), "\n"))
}

type Client struct {
	base    string
	token   string
	cloud   bool
	verbose bool
	hc      *http.Client
}

func normalizeAddr(addr string) string {
	addr = strings.TrimRight(strings.TrimSpace(addr), "/")
	if !strings.HasPrefix(addr, "http://") && !strings.HasPrefix(addr, "https://") {
		addr = "http://" + addr
	}
	return addr
}

func NewClient(addr, token string, cloud bool, timeout time.Duration, verbose bool) *Client {
	c := &Client{token: token, cloud: cloud, verbose: verbose, hc: &http.Client{Timeout: timeout}}
	switch {
	case cloud && addr != "":
		c.base = normalizeAddr(addr)
	case cloud:
		c.base = cloudBase
	default:
		if addr == "" {
			addr = defaultAddr
		}
		c.base = normalizeAddr(addr) + localPrefix
	}
	return c
}

func apiVersion() string {
	if v := os.Getenv("BUSY_API_VERSION"); v != "" {
		return v
	}
	return defaultAPIVer
}

// opts carries the optional parts of a request. The zero value is a plain call.
type opts struct {
	params  map[string]any
	json    any
	data    []byte
	raw     bool          // return the response body untouched
	timeout time.Duration // per-request override (uploads)
}

func paramValue(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case int:
		return strconv.Itoa(t)
	case float64:
		return strconv.FormatFloat(t, 'g', -1, 64)
	default:
		return fmt.Sprint(t)
	}
}

func (c *Client) do(method, path string, o opts) (any, error) {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	full := c.base + path
	if len(o.params) > 0 {
		q := url.Values{}
		for k, v := range o.params {
			if v == nil {
				continue
			}
			q.Set(k, paramValue(v))
		}
		if len(q) > 0 {
			full += "?" + q.Encode()
		}
	}

	body, ctype := o.data, ""
	if o.json != nil {
		b, err := json.Marshal(o.json)
		if err != nil {
			return nil, err
		}
		body, ctype = b, "application/json"
	} else if body != nil {
		ctype = "application/octet-stream"
	}

	method = strings.ToUpper(method)
	if c.verbose {
		note("> %s %s", method, full)
		if o.json != nil {
			b, _ := json.Marshal(o.json)
			note("> %s", b)
		} else if len(body) > 0 {
			note("> <%d bytes>", len(body))
		}
	}

	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, full, reader)
	if err != nil {
		return nil, fail("%v", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set(apiVersionHdr, apiVersion())
	req.Header.Set("User-Agent", "busybar-cli/"+version)
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	if c.token != "" {
		if c.cloud {
			req.Header.Set("Authorization", "Bearer "+c.token)
		} else {
			req.Header.Set("X-API-Token", c.token)
		}
	}

	hc := c.hc
	if o.timeout > 0 {
		hc = &http.Client{Timeout: o.timeout}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fail("could not reach %s: %v", full, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fail("reading response from %s: %v", full, err)
	}

	if resp.StatusCode >= 400 {
		detail := strings.TrimSpace(string(raw))
		if resp.StatusCode == http.StatusForbidden {
			detail += "\nhint: this bar wants authentication. Pass --token " +
				"(access key over Wi-Fi, API token in cloud mode)."
		}
		return nil, &APIError{resp.StatusCode, method, full, strings.TrimSpace(detail)}
	}

	if o.raw {
		return raw, nil
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	// The device lies about Content-Type, so try JSON regardless and fall back to text.
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err == nil {
		return decoded, nil
	}
	return string(raw), nil
}

func (c *Client) get(path string, o opts) (any, error)    { return c.do("GET", path, o) }
func (c *Client) post(path string, o opts) (any, error)   { return c.do("POST", path, o) }
func (c *Client) put(path string, o opts) (any, error)    { return c.do("PUT", path, o) }
func (c *Client) delete(path string, o opts) (any, error) { return c.do("DELETE", path, o) }
