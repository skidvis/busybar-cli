create a cli for this api: https://docs.busy.app/bar/dev


Debugged argument ordering and verified CLI functionality
Debugged argument ordering and verified CLI functionality
A single-file, dependency-free CLI covering the BUSY Bar HTTP API.

I pulled the endpoint surface from the docs plus the official busylib package (installed and introspected it) so the paths, params, and payload shapes match the real API rather than being guessed — all 48 documented paths are reachable, and raw covers anything I didn't wrap in a friendly command.

Highlights:

Three transports: USB (10.0.4.20, no auth), Wi-Fi (X-API-Token), cloud (api.busy.app/busybar, Bearer) — selected via --addr/--cloud, env vars, or a saved config.
Display commands build proper DisplayElements payloads for text, images/animations, countdowns, and rectangles, with a warning when coordinates fall outside the 72×16 front or 160×80 back panel.
display screenshot decodes the base64 framebuffer the device returns (RGB888 front, packed 4-bit greyscale back — low nibble is the left pixel) and writes a real PNG, with optional upscaling since a 72×16 image is otherwise unviewable.
Storage, assets, audio, timer profiles, Wi-Fi, BLE, time, updates, account, and Matter switch are all wrapped.
Untested against real hardware — I ran it against a mock server, so the URLs, headers, and bodies are verified but the device's responses aren't. Two known gaps documented in the README: asset uploads aren't converted client-side, and there's no WebSocket support for live state or screen streaming, since the standard library has no WS client.


Busybar
PY 

Readme
Document · MD 
how does this do it? https://instantcli.com/


Synthesized API specs into automated CLI generation pipeline
Synthesized API specs into automated CLI generation pipeline
Short version: it's a doc-crawl → LLM → Go codegen → binary-hosting pipeline.

The steps they describe:

Crawl. You paste a docs URL (or OpenAPI spec) and they crawl every page to discover routes, methods, parameters, and context. The pitch is that they extract context from human-written docs, not just OpenAPI specs — so prose like "this endpoint requires a 4–10 digit access key" becomes help text, not just a --key string flag. 
instantcli
instantcli
Map. An LLM turns endpoints into a cli <resource> <action> --flags tree — users list, users get --id user_123, users create --name "Jane" — plus a standard global set (--api-key, --base-url, --output table|json|raw) and a configure command for storing credentials.
Compile and host. Everything is generated in Go, chosen over Node frameworks like oclif because Go compiles to standalone binaries with no runtime. You get macOS (Intel + Apple Silicon), Linux (x86_64 + ARM64), and Windows x86_64, a hosted curl -fsSL get.instantcli.com/your-slug/install.sh | sh, and a GitHub repo you own.
Re-crawl on change. They diff new docs against the last crawled version, regenerate only what changed, and push an update; the binary checks in the background at most once every four hours and tells you when a new version exists.
~7 minutes average, $9 one-time. 
instantcli

How that compares to what I just did: steps 1 and 2 are exactly what I did by hand — fetch the docs pages, install busylib and introspect it to recover the real paths and payload shapes, then map them onto a command tree. The difference is in both directions.

They win on distribution. A single Go binary with a curl-installer and auto-update is meaningfully better than a Python file you symlink yourself, especially for the agent use case they're targeting.

They'd likely lose on the parts that aren't in the docs. A mechanical endpoint→command mapping would have given you busybar screen --display 1 returning base64 garbage to your terminal, because the BUSY Bar's /api/screen sends Content-Type: image/bmp while actually returning a base64-wrapped raw framebuffer — and the back display's 4-bit greyscale packs the left pixel in the low nibble, which is documented nowhere except a source comment and a community project's bugfix note. The display screenshot command that writes a real PNG isn't derivable from the docs; it came from reading the client library's decoder. Same for the three-transport auth switching and the coordinate bounds warnings.

So: good fit for a large, well-specified REST API where the endpoints map cleanly and you want it installable everywhere. Less good where the interesting work is in the gap between what the docs say and what the device does.

can you do it similarly in go? if so, generate a document on how to do it for me to pass to an llm