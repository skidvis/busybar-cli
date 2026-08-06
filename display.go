package main

import (
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var (
	fonts = []string{"tiny", "small", "normal", "condensed", "bold", "large", "extra_large", "global"}
	// The middle cell is "center", not "mid_mid" (busylib DisplayElementBase.align).
	alignments = []string{
		"top_left", "top_mid", "top_right",
		"mid_left", "center", "mid_right",
		"bottom_left", "bottom_mid", "bottom_right",
	}
)

func choice(flag, value string, allowed ...string) error {
	for _, a := range allowed {
		if a == value {
			return nil
		}
	}
	return fail("--%s must be one of: %s (got %q)", flag, strings.Join(allowed, ", "), value)
}

// parseColor accepts "#rrggbb", "#rrggbbaa", bare hex, "r,g,b" or "r,g,b,a" and
// normalizes to the "#RRGGBBAA" string the API wants. Alpha defaults to FF.
// The device takes a string, never an [r,g,b] array.
func parseColor(value string) (string, error) {
	value = strings.TrimSpace(value)

	if strings.Contains(value, ",") {
		parts := strings.Split(value, ",")
		if len(parts) != 3 && len(parts) != 4 {
			return "", fail("color %q must be r,g,b or r,g,b,a", value)
		}
		channels := [4]int{0, 0, 0, 255}
		for i, p := range parts {
			n, err := strconv.Atoi(strings.TrimSpace(p))
			if err != nil {
				return "", fail("color components must be integers: %s", value)
			}
			if n < 0 || n > 255 {
				return "", fail("color component %d out of range 0-255: %s", n, value)
			}
			channels[i] = n
		}
		return fmt.Sprintf("#%02X%02X%02X%02X", channels[0], channels[1], channels[2], channels[3]), nil
	}

	hex := strings.ToUpper(strings.TrimPrefix(value, "#"))
	for _, c := range hex {
		if !strings.ContainsRune("0123456789ABCDEF", c) {
			return "", fail("color %q is not hex", value)
		}
	}
	switch len(hex) {
	case 6:
		return "#" + hex + "FF", nil
	case 8:
		return "#" + hex, nil
	default:
		return "", fail("color %q must be #rrggbb or #rrggbbaa", value)
	}
}

// elemFlags are the options every display element shares.
type elemFlags struct {
	app, id, display, align, until, ledColor string
	lifetime                                 time.Duration
	x, y, priority                           int
	clear                                    bool
}

func (e *elemFlags) attach(cmd *cobra.Command, defaultDisplay string) {
	f := cmd.Flags()
	f.StringVar(&e.app, "app", "busybar-cli", "application_name the drawing is grouped under")
	f.StringVar(&e.id, "id", "", "element id (default: derived from the command)")
	f.StringVarP(&e.display, "display", "d", defaultDisplay, "front|back")
	f.IntVarP(&e.x, "x", "x", 0, "x position")
	f.IntVarP(&e.y, "y", "y", 0, "y position")
	f.StringVar(&e.align, "align", "", "one of "+strings.Join(alignments, ", "))
	f.DurationVar(&e.lifetime, "for", 0, "auto-remove the element after this long, e.g. 10s or 2m")
	f.StringVar(&e.until, "display-until", "", "hide the element at this Unix timestamp (seconds); excludes --for")
	f.IntVar(&e.priority, "priority", 0, "draw priority")
	f.StringVar(&e.ledColor, "led-color", "", "LED notification color, #rrggbb or #rrggbbaa")
	f.BoolVar(&e.clear, "clear", false, "clear this app's elements before drawing")
}

func (e *elemFlags) base(cmd *cobra.Command, defaultID string) (map[string]any, error) {
	if err := choice("display", e.display, "front", "back"); err != nil {
		return nil, err
	}
	id := e.id
	if id == "" {
		id = defaultID
	}
	el := map[string]any{"id": id, "x": e.x, "y": e.y, "display": e.display}
	if e.align != "" {
		if !slices.Contains(alignments, e.align) {
			return nil, fail("--align must be one of: %s (got %q)",
				strings.Join(alignments, ", "), e.align)
		}
		el["align"] = e.align
		// align picks which point OF THE ELEMENT is the anchor; x/y then place
		// that anchor on the panel (openapi.yaml: "X coordinate of selected
		// anchor point relative to top-left of display"). So --align center with
		// the default x=0,y=0 pins the element's middle to the top-left corner.
		// Default the coordinates to the matching point on the panel instead.
		ax, ay := anchorPoint(e.align, displays[e.display])
		if !cmd.Flags().Changed("x") {
			el["x"] = ax
		}
		if !cmd.Flags().Changed("y") {
			el["y"] = ay
		}
	}
	if cmd.Flags().Changed("for") && e.until != "" {
		return nil, fail("--for and --display-until are mutually exclusive")
	}
	if cmd.Flags().Changed("for") {
		// openapi.yaml: "Time in seconds the element should be displayed".
		secs := int(e.lifetime.Round(time.Second) / time.Second)
		if secs < 1 {
			return nil, fail("--for must be at least 1s (the device counts whole seconds)")
		}
		el["timeout"] = secs
	}
	if e.until != "" {
		if _, err := strconv.Atoi(e.until); err != nil {
			return nil, fail("--display-until must be a Unix timestamp in seconds, got %q", e.until)
		}
		el["display_until"] = e.until
	}
	return el, nil
}

// anchorPoint returns the point on the panel that matches an align value, so
// "center" lands mid-panel and "bottom_right" lands in the bottom-right corner.
// Align strings are <vertical>_<horizontal>, except the bare "center".
func anchorPoint(align string, spec displaySpec) (x, y int) {
	vertical, horizontal := "mid", "mid"
	if align != "center" {
		vertical, horizontal, _ = strings.Cut(align, "_")
	}
	switch horizontal {
	case "right":
		x = spec.width
	case "mid":
		x = spec.width / 2
	}
	switch vertical {
	case "bottom":
		y = spec.height
	case "mid":
		y = spec.height / 2
	}
	return x, y
}

// warnBounds nags on stderr about elements that start off-screen. Silent no-op
// draws are the most common confusion with this device, so never fail on it.
func warnBounds(elements []map[string]any) {
	for _, el := range elements {
		// With an anchor set, x/y address a point on the element, so a
		// right/bottom anchor legitimately sits at the panel edge.
		if _, anchored := el["align"]; anchored {
			continue
		}
		name, _ := el["display"].(string)
		if name == "" {
			name = "front"
		}
		spec, ok := displays[name]
		if !ok {
			continue
		}
		x, _ := el["x"].(int)
		y, _ := el["y"].(int)
		if x >= spec.width || y >= spec.height {
			note("warning: element %v starts outside the %s display (%dx%d)",
				el["id"], name, spec.width, spec.height)
		}
	}
}

func sendDraw(c *Client, cmd *cobra.Command, e *elemFlags, elements []map[string]any) (any, error) {
	warnBounds(elements)
	payload := map[string]any{"application_name": e.app, "elements": elements}
	if cmd.Flags().Changed("priority") {
		payload["priority"] = e.priority
	}
	if e.ledColor != "" {
		col, err := parseColor(e.ledColor)
		if err != nil {
			return nil, err
		}
		payload["led_notification_color"] = col
	}
	if e.clear {
		if _, err := c.delete("/display/draw", opts{params: map[string]any{"application_name": e.app}}); err != nil {
			return nil, err
		}
	}
	return c.post("/display/draw", opts{json: payload})
}

func displayCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "display", Short: "draw on the screens"}
	cmd.AddCommand(displayText(), displayImage(), displayCountdown(), displayRect(),
		displayDraw(), displayClear(), displayBrightness(), displayScreenshot())
	return cmd
}

func displayText() *cobra.Command {
	var e elemFlags
	var font, color string
	var width, rate, startDelay, repeatDelay int

	c := &cobra.Command{
		Use:   "text <text>",
		Short: "show text",
		Args:  cobra.ExactArgs(1),
		RunE: run(func(cl *Client, cmd *cobra.Command, args []string) (any, error) {
			if err := choice("font", font, fonts...); err != nil {
				return nil, err
			}
			el, err := e.base(cmd, "text")
			if err != nil {
				return nil, err
			}
			// openapi.yaml TextElement.text: ^[\x20-\x7E]+$ - the fonts are
			// bitmap ASCII, so anything else is a 400 from the device.
			for _, r := range args[0] {
				if r < 0x20 || r > 0x7E {
					return nil, fail("text must be printable ASCII (the fonts are bitmap ASCII); %q is not", r)
				}
			}
			el["type"] = "text"
			el["text"] = args[0]
			el["font"] = font
			if color != "" {
				col, err := parseColor(color)
				if err != nil {
					return nil, err
				}
				el["color"] = col
			}
			for name, val := range map[string]int{
				"width": width, "scroll_rate": rate,
				"scroll_start_delay": startDelay, "scroll_repeat_delay": repeatDelay,
			} {
				if cmd.Flags().Changed(strings.ReplaceAll(name, "_", "-")) {
					el[name] = val
				}
			}
			return sendDraw(cl, cmd, &e, []map[string]any{el})
		}),
	}
	f := c.Flags()
	f.StringVar(&font, "font", "small", "one of "+strings.Join(fonts, ", "))
	f.StringVar(&color, "color", "", "#rrggbb, #rrggbbaa, r,g,b or r,g,b,a")
	f.IntVar(&width, "width", 0, "clip width, enables scrolling")
	f.IntVar(&rate, "scroll-rate", 0, "")
	f.IntVar(&startDelay, "scroll-start-delay", 0, "")
	f.IntVar(&repeatDelay, "scroll-repeat-delay", 0, "")
	e.attach(c, "front")
	return c
}

func displayImage() *cobra.Command {
	var e elemFlags
	var stock, animation, loop bool
	var opacity int

	c := &cobra.Command{
		Use:   "image <path>",
		Short: "show an uploaded image or animation",
		Args:  cobra.ExactArgs(1),
		RunE: run(func(cl *Client, cmd *cobra.Command, args []string) (any, error) {
			el, err := e.base(cmd, "image")
			if err != nil {
				return nil, err
			}
			el["type"] = "image"
			if animation {
				el["type"] = "animation"
				el["loop"] = loop
			}
			if stock {
				el["stock_path"] = args[0]
			} else {
				el["path"] = args[0]
			}
			if cmd.Flags().Changed("opacity") {
				el["opacity"] = opacity
			}
			return sendDraw(cl, cmd, &e, []map[string]any{el})
		}),
	}
	f := c.Flags()
	f.BoolVar(&stock, "stock", false, "path refers to a built-in asset")
	f.BoolVar(&animation, "animation", false, "draw as an animation element")
	f.BoolVar(&loop, "loop", false, "loop the animation")
	f.IntVar(&opacity, "opacity", 0, "0-100")
	e.attach(c, "back")
	return c
}

func displayCountdown() *cobra.Command {
	var e elemFlags
	var direction, showHours, color string

	c := &cobra.Command{
		Use:   "countdown <unix-seconds>",
		Short: "show a live countdown to/from a Unix timestamp in seconds",
		Args:  cobra.ExactArgs(1),
		RunE: run(func(cl *Client, cmd *cobra.Command, args []string) (any, error) {
			// openapi.yaml CountdownElement.timestamp: ^[0-9]+$, and it must be
			// sent as a string, not a number.
			if _, err := strconv.Atoi(args[0]); err != nil {
				return nil, fail("countdown takes a Unix timestamp in seconds, got %q", args[0])
			}
			if err := choice("direction", direction, "time_left", "time_since"); err != nil {
				return nil, err
			}
			if err := choice("show-hours", showHours, "when_non_zero", "always"); err != nil {
				return nil, err
			}
			el, err := e.base(cmd, "countdown")
			if err != nil {
				return nil, err
			}
			el["type"] = "countdown"
			el["timestamp"] = args[0]
			el["direction"] = direction
			el["show_hours"] = showHours
			if color != "" {
				col, err := parseColor(color)
				if err != nil {
					return nil, err
				}
				el["color"] = col
			}
			return sendDraw(cl, cmd, &e, []map[string]any{el})
		}),
	}
	f := c.Flags()
	f.StringVar(&direction, "direction", "time_left", "time_left|time_since")
	f.StringVar(&showHours, "show-hours", "when_non_zero", "when_non_zero|always")
	f.StringVar(&color, "color", "", "#rrggbb, #rrggbbaa, r,g,b or r,g,b,a")
	e.attach(c, "front")
	return c
}

func displayRect() *cobra.Command {
	var e elemFlags
	var fill, borderColor string
	var fillColors []string
	var width, height, radius, borderWidth int

	c := &cobra.Command{
		Use:   "rect",
		Short: "draw a rectangle",
		Args:  cobra.NoArgs,
		RunE: run(func(cl *Client, cmd *cobra.Command, args []string) (any, error) {
			if err := choice("fill", fill, "none", "solid", "gradient_h", "gradient_v"); err != nil {
				return nil, err
			}
			el, err := e.base(cmd, "rect")
			if err != nil {
				return nil, err
			}
			el["type"] = "rectangle"
			el["width"] = width
			el["height"] = height
			el["fill"] = fill
			if cmd.Flags().Changed("radius") {
				el["radius"] = radius
			}
			if len(fillColors) > 0 {
				cols := make([]string, 0, len(fillColors))
				for _, raw := range fillColors {
					col, err := parseColor(raw)
					if err != nil {
						return nil, err
					}
					cols = append(cols, col)
				}
				el["fill_colors"] = cols
			}
			if cmd.Flags().Changed("border-width") {
				el["border_width"] = borderWidth
			}
			if borderColor != "" {
				col, err := parseColor(borderColor)
				if err != nil {
					return nil, err
				}
				el["border_color"] = col
			}
			return sendDraw(cl, cmd, &e, []map[string]any{el})
		}),
	}
	f := c.Flags()
	f.IntVar(&width, "width", 0, "rectangle width")
	f.IntVar(&height, "height", 0, "rectangle height")
	f.IntVar(&radius, "radius", 0, "corner radius")
	f.StringVar(&fill, "fill", "solid", "none|solid|gradient_h|gradient_v")
	// StringArray, not StringSlice: slices comma-split, which would shred "r,g,b".
	f.StringArrayVar(&fillColors, "fill-colors", nil, "repeat for a gradient: one color for solid, two for a gradient")
	f.IntVar(&borderWidth, "border-width", 0, "")
	f.StringVar(&borderColor, "border-color", "", "#rrggbb, #rrggbbaa, r,g,b or r,g,b,a")
	_ = c.MarkFlagRequired("width")
	_ = c.MarkFlagRequired("height")
	e.attach(c, "front")
	return c
}

func displayDraw() *cobra.Command {
	var app string
	var clear bool

	c := &cobra.Command{
		Use:   "draw <payload>",
		Short: "POST a raw display payload (JSON literal, @file.json, or -)",
		Args:  cobra.ExactArgs(1),
		RunE: run(func(cl *Client, cmd *cobra.Command, args []string) (any, error) {
			parsed, err := readJSONArg(args[0])
			if err != nil {
				return nil, err
			}
			payload, ok := parsed.(map[string]any)
			if !ok {
				list, isList := parsed.([]any)
				if !isList {
					return nil, fail("draw payload must be an object or a list of elements")
				}
				payload = map[string]any{"elements": list}
			}
			if _, ok := payload["application_name"]; !ok || cmd.Flags().Changed("app") {
				payload["application_name"] = app
			}
			if raw, ok := payload["elements"].([]any); ok {
				elements := make([]map[string]any, 0, len(raw))
				for _, item := range raw {
					if el, ok := item.(map[string]any); ok {
						// JSON numbers decode as float64; warnBounds wants ints.
						for _, k := range []string{"x", "y"} {
							if n, ok := el[k].(float64); ok {
								el[k] = int(n)
							}
						}
						elements = append(elements, el)
					}
				}
				warnBounds(elements)
			}
			if clear {
				params := map[string]any{"application_name": payload["application_name"]}
				if _, err := cl.delete("/display/draw", opts{params: params}); err != nil {
					return nil, err
				}
			}
			return cl.post("/display/draw", opts{json: payload})
		}),
	}
	c.Flags().StringVar(&app, "app", "busybar-cli", "application_name")
	c.Flags().BoolVar(&clear, "clear", false, "clear this app's elements first")
	return c
}

func displayClear() *cobra.Command {
	var app string
	c := &cobra.Command{
		Use:   "clear",
		Short: "clear drawn elements",
		Args:  cobra.NoArgs,
		RunE: run(func(cl *Client, cmd *cobra.Command, args []string) (any, error) {
			var params map[string]any
			if app != "" {
				params = map[string]any{"application_name": app}
			}
			return cl.delete("/display/draw", opts{params: params})
		}),
	}
	c.Flags().StringVar(&app, "app", "", "limit to one application_name")
	return c
}

func displayBrightness() *cobra.Command {
	return &cobra.Command{
		Use:   "brightness [0-100|auto]",
		Short: "get or set brightness",
		Args:  cobra.MaximumNArgs(1),
		RunE: run(func(cl *Client, cmd *cobra.Command, args []string) (any, error) {
			if len(args) == 0 {
				return cl.get("/display/brightness", opts{})
			}
			var value any = args[0]
			if args[0] != "auto" {
				n, err := strconv.Atoi(args[0])
				if err != nil || n < 0 || n > 100 {
					return nil, fail("brightness must be 0-100 or 'auto'")
				}
				value = n
			}
			return cl.post("/display/brightness", opts{params: map[string]any{"value": value}})
		}),
	}
}

func displayScreenshot() *cobra.Command {
	var out string
	var scale int

	c := &cobra.Command{
		Use:   "screenshot [front|back]",
		Short: "capture a display as PNG",
		Args:  cobra.MaximumNArgs(1),
		RunE: run(func(cl *Client, cmd *cobra.Command, args []string) (any, error) {
			which := "front"
			if len(args) == 1 {
				which = args[0]
			}
			spec, ok := displays[which]
			if !ok {
				return nil, fail("display must be front or back (got %q)", which)
			}
			body, err := cl.get("/screen", opts{params: map[string]any{"display": spec.index}, raw: true})
			if err != nil {
				return nil, err
			}
			rgb, err := decodeFrame(body.([]byte), spec.format, spec.width, spec.height)
			if err != nil {
				return nil, err
			}
			if scale < 1 {
				scale = 1
			}
			data, err := framePNG(rgb, spec.width, spec.height, scale)
			if err != nil {
				return nil, err
			}
			if out == "-" {
				return data, nil
			}
			if err := os.WriteFile(out, data, 0o644); err != nil {
				return nil, fail("%v", err)
			}
			msg := fmt.Sprintf("wrote %s (%dx%d", out, spec.width*scale, spec.height*scale)
			if scale > 1 {
				msg += fmt.Sprintf(", scaled %dx", scale)
			}
			return msg + ")", nil
		}),
	}
	c.Flags().StringVarP(&out, "out", "o", "screenshot.png", "output file, or - for stdout")
	c.Flags().IntVar(&scale, "scale", 1, "nearest-neighbour upscale factor")
	return c
}
