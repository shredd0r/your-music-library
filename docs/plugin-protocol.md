# ymlib plugin protocol (version 1)

A **plugin** is a download source for ymlib: something that can search for tracks and albums and hand over audio files. A plugin is an **HTTP server that you run yourself**, in any language. ymlib never starts, stops or installs plugin code. It only keeps the plugin's address and acts as an HTTP client.

ymlib doesn't ship plugins for any service. Plugin authors are responsible for using services in line with their terms.

## Running and registering

1. Start your plugin server (a terminal, your IDE, launchd/systemd, Docker: your choice). Listen on `127.0.0.1` unless you really want it reachable from other machines. There is no authentication, so anyone who can reach the port can use the plugin.
2. Register it once:
   ```sh
   ymlib plugin add myplugin http://127.0.0.1:8765
   ymlib plugin list        # name, URL, enabled, online/offline
   ```
   The name you choose (`myplugin`) appears in every reference (`myplugin:<id>`) and in queued jobs. It may not contain `:`, `/` or spaces.
3. `ymlib plugin disable|enable|remove myplugin` manage the registration, which is stored in `config.yaml` under `plugins.registered`.

A plugin keeps whatever state it needs (logins, sessions, caches, open connections) in its own memory, for as long as it runs.

## Endpoints

All requests and answers are JSON (`Content-Type: application/json`), except the body of a successful download. Paths are relative to the registered URL.

### `GET /health`

Who the plugin is. ymlib calls it when you register the plugin, for `ymlib plugin list`, once per download run, and every 15 seconds while the TUI is open (2-second timeout).

```json
{"name": "myplugin", "version": "1.2.0", "protocol": 1}
```

`protocol` must be `1`. A plugin answering another version is refused. `name` and `version` are only shown.

### `POST /search`

```json
{"query": "daft punk one more time", "kind": "track", "limit": 20}
```
→
```json
{"results": [Item, ...]}
```

`kind` is `track` or `album`. Return at most `limit` results.

### `POST /resolve`

```json
{"id": "abc123", "kind": "album"}
```
→
```json
{"item": Item, "tracks": [Item, ...]}
```

For an album, `tracks` lists its tracks in order. For a track, `tracks` may be empty; ymlib then uses `item`.

### `POST /download`

```json
{"id": "track-42"}
```
→ **the audio file itself as the response body**, with:

| Header | |
|---|---|
| `Content-Type` | e.g. `audio/flac`, `audio/mpeg` |
| `Content-Length` | the file size; strongly recommended, ymlib uses it to detect cut-off downloads |
| `Content-Disposition` | `attachment; filename="01 - One More Time.flac"`; optional, otherwise the name is `<id>.<ext from Content-Type>` |

Stream the file; don't read it into memory. Files of 250 MB and more are normal for hi-res audio, and ymlib writes the body straight to disk. Only the base name of `filename` is used. ymlib checks the file (it must decode, and its length must match `duration_sec`) before it tags and files it. ymlib handles FLAC and MP3.

## Item

```json
{
  "id": "track-42",
  "kind": "track",
  "title": "One More Time",
  "artist": "Daft Punk",
  "album": "Discovery",
  "album_artist": "Daft Punk",
  "year": 2001,
  "track_number": 1,
  "disc_number": 1,
  "track_count": 14,
  "duration_sec": 320,
  "isrc": "GBDUW0000053",
  "explicit": false,
  "cover_url": "https://example.com/cover.jpg",
  "quality": {"format": "flac", "bit_depth": 24, "sample_rate": 96000}
}
```

Only `id`, `kind`, `title`, `artist` and `quality.format` are required. The rest helps ymlib spot duplicates (`isrc`, `album`, `track_number`), check downloads (`duration_sec`) and identify releases. For lossy formats use `"quality": {"format": "mp3", "bitrate_kbps": 320}`.

## Errors

Answer with a non-2xx status and:

```json
{"error": {"code": "not_found", "message": "no track track-42"}}
```

| `code` | Meaning | ymlib does |
|---|---|---|
| `not_found` | the id doesn't exist | fails the job, no retry |
| `permanent` | it will never work (region-locked, unavailable, bad request) | fails the job, no retry |
| `temporary` | try again later (network trouble, rate limit) | retries with backoff, up to 3 attempts |

Any other code, a missing error body, or a broken answer counts as `temporary`.

## When the plugin is offline

If the server doesn't answer (not started, crashed, wrong port):

- **Search** skips it and warns that it is offline.
- **Queued downloads wait** without using up their attempts. Each queue run (`ymlib queue run`, a new download, or the TUI noticing the plugin is back) tries them again. After `plugins.offline_wait_minutes` (default 60) they fail with "offline"; `ymlib queue retry` brings them back.
- A **disabled** plugin is treated the same way: its downloads wait until you enable it.

## Try it with curl

```sh
curl http://127.0.0.1:8765/health
curl -X POST -H 'Content-Type: application/json' -d '{"query":"daft","kind":"album","limit":5}' http://127.0.0.1:8765/search
curl -X POST -H 'Content-Type: application/json' -d '{"id":"track-42"}' -OJ http://127.0.0.1:8765/download
```

## A minimal plugin in Python

Standard library only. It offers the FLAC files in one folder; a real plugin would talk to its service here and keep its session in the handler's module state.

```python
import json, os, sys
from http.server import ThreadingHTTPServer, BaseHTTPRequestHandler

FOLDER = sys.argv[2] if len(sys.argv) > 2 else "."

def items():
    for name in sorted(os.listdir(FOLDER)):
        if name.endswith(".flac"):
            stem = name[:-5]
            yield {"id": stem, "kind": "track", "title": stem, "artist": "Unknown",
                   "quality": {"format": "flac"}}

class Plugin(BaseHTTPRequestHandler):
    def answer(self, status, value):
        body = json.dumps(value).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def error(self, status, code, message):
        self.answer(status, {"error": {"code": code, "message": message}})

    def do_GET(self):
        if self.path == "/health":
            return self.answer(200, {"name": "folder", "version": "0.1", "protocol": 1})
        self.error(404, "permanent", "no such endpoint")

    def do_POST(self):
        req = json.loads(self.rfile.read(int(self.headers.get("Content-Length", 0))) or b"{}")
        found = {it["id"]: it for it in items()}
        if self.path == "/search":
            q = req.get("query", "").lower()
            hits = [it for it in found.values() if req.get("kind") == "track" and q in it["title"].lower()]
            return self.answer(200, {"results": hits[: req.get("limit", 20)]})
        if self.path in ("/resolve", "/download") and req.get("id") not in found:
            return self.error(404, "not_found", "unknown id " + str(req.get("id")))
        if self.path == "/resolve":
            return self.answer(200, {"item": found[req["id"]], "tracks": []})
        if self.path == "/download":
            path = os.path.join(FOLDER, req["id"] + ".flac")
            self.send_response(200)
            self.send_header("Content-Type", "audio/flac")
            self.send_header("Content-Length", str(os.path.getsize(path)))
            self.send_header("Content-Disposition", f'attachment; filename="{req["id"]}.flac"')
            self.end_headers()
            with open(path, "rb") as f:          # streamed in 1 MB pieces
                while chunk := f.read(1 << 20):
                    self.wfile.write(chunk)
            return
        self.error(404, "permanent", "no such endpoint")

# ThreadingHTTPServer answers parallel downloads; port first, then the folder
ThreadingHTTPServer(("127.0.0.1", int(sys.argv[1])), Plugin).serve_forever()
```

```sh
python3 folder_plugin.py 8765 ~/Music/Rips &
ymlib plugin add folder http://127.0.0.1:8765
ymlib search folder-track-name
```

Any HTTP framework works the same way (aiohttp, FastAPI, Flask, Go's net/http, Node's http). The framework handles concurrency, so there's no request loop to write by hand.
