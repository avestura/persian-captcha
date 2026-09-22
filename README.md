# Persian Captcha

An interactive captcha service. Other services call it to verify that a
visitor is human, using slider puzzles, rotation dials, ordered clicks and
drag-and-drop — not distorted letters.

The widget speaks English and Persian out of the box, mirrors its layout for
right-to-left reading, and renders Persian numerals where a Persian reader
expects them. Adding a language is one JSON file.

```
┌─ visitor's browser ────────────┐        ┌─ your backend ──────────┐
│  your page                     │        │                         │
│    └─ widget.js                │        │   POST /v1/siteverify   │
│         └─ iframe (captcha     │        │        secret + token   │
│            service origin)     │        │              ↓          │
│               puzzle + token ──┼── form ┼──→ token → verdict      │
└────────────────────────────────┘        └─────────────────────────┘
```

## What it does

* **Four interactive challenges**, all drawn procedurally at request time:
  a jigsaw slider, a rotate-to-upright dial, click-the-icons-in-order, and
  drag-each-shape-into-its-outline.
* **A checkbox gate.** Most visitors click once and are through. A puzzle
  appears only when the risk score warrants it, or for a sampled fraction of
  traffic.
* **Behavioural scoring.** The answer alone is not enough: the pointer path,
  its timing and its shape are graded too. A script that computes the right
  slider offset and delivers it in a straight line at even intervals is
  refused.
* **Proof of work.** Every challenge and every retry costs the browser real
  CPU, which changes the arithmetic for anyone solving captchas in bulk.
* **A genuine accessible fallback.** Sliders and drag targets are unusable
  with a screen reader, so there is a keyboard-operable, localised question
  behind a link on every challenge.
* **No tracking.** No canvas or WebGL fingerprinting, no font probing, no
  cross-site identifier, no cookies. Addresses are hashed with a per-process
  secret and everything expires within minutes.

## Try it

Every challenge is playable in the browser, with no install, at
<https://avestura.github.io/persian-captcha/>. The page runs the real widget
components against fixed artwork, so what you drag there is what your visitors
drag.

To run the service itself:

```sh
docker compose -f docker-compose.demo.yml up --build
```

Or, with a Go toolchain and no containers:

```sh
go run ./cmd/captchad -config captcha.demo.yaml       # the service, :8080
go run ./cmd/demo                                     # a site using it, :5173
```

Either way, open <http://localhost:5173>. The page is a gallery: one widget
per challenge kind, then the same puzzles at each difficulty, then one card
configured the way a real site would be. Each card is its own form and is
verified against its own secret. The site keys behind it live in
`captcha.demo.yaml`, where each one is pinned to a single kind — which is what
makes the page predictable, and is not something to copy into production.

The accessible question is the one kind no site key can pin: it is offered
behind a link on every puzzle rather than imposed, so its card asks you to
follow that link.

To see the artwork without running anything:

```sh
go run ./cmd/preview -out ./preview
```

## Integrating

Two steps: put the widget on your page, and check the token on your server.
Full details, including React, Vue and backend examples in several languages,
are in [docs/INTEGRATION.md](docs/INTEGRATION.md).

### On the page

```html
<form method="post" action="/signup">
  <input name="email" type="email" required>

  <div class="pcaptcha" data-sitekey="pc_site_demo_en"></div>

  <button type="submit">Sign up</button>
</form>

<script src="https://captcha.example.com/v1/widget.js" async defer></script>
```

The widget adds a hidden `pcaptcha-response` field to the surrounding form.

### On your server

```go
form := url.Values{"secret": {os.Getenv("CAPTCHA_SECRET")},
    "response": {r.FormValue("pcaptcha-response")}}
res, err := http.PostForm("https://captcha.example.com/v1/siteverify", form)
// decode; reject the request unless success is true
```

The reply is the same shape reCAPTCHA and hCaptcha return, so an existing
integration usually needs only its URL and keys changed:

```json
{
  "success": true,
  "challenge_ts": "2026-09-19T22:22:45Z",
  "hostname": "app.example.com",
  "score": 0.94,
  "solved": "slider_jigsaw"
}
```

A token is **single use**. The second redemption of the same token fails with
`timeout-or-duplicate`, which is what stops a captured response being replayed.

## Configuring

Site keys live in a configuration file — there is no database and no admin
API, which keeps deployments GitOps-friendly and the attack surface small. The
file is re-read when it changes, so keys and origins rotate without a restart.
See [`captcha.example.yaml`](captcha.example.yaml) for a commented example.

```yaml
sites:
  - name: "Example"
    key: "pc_site_example"
    secret: "pc_secret_example"
    allowed_origins: ["https://app.example.com"]
    locale: "fa"
    difficulty: "normal"
    challenge_rate: 0.25
```

Every setting also has an environment variable, so a container needs no file
at all:

```sh
PCAPTCHA_SITE_KEY=... PCAPTCHA_SITE_SECRET=... captchad
```

| Variable | Effect |
| --- | --- |
| `PCAPTCHA_CONFIG` | Path to the configuration file |
| `PCAPTCHA_LISTEN` | Listen address, default `:8080` |
| `PCAPTCHA_REDIS_ADDR` | Enables the Redis store and points at it |
| `PCAPTCHA_TRUST_PROXY` | Trust `X-Forwarded-For`; see the warning below |
| `PCAPTCHA_SITE_KEY` / `PCAPTCHA_SITE_SECRET` | Declare a single site |
| `PCAPTCHA_SITE_ORIGINS` | Comma-separated origin allow-list |
| `PCAPTCHA_DEFAULT_LOCALE` | Fallback language, default `en` |

`captchad -check` validates a configuration and exits, which is worth running
in CI against whatever you are about to deploy.

### Two things worth getting right

**`allowed_origins`.** An empty list means any site may embed that key. That
is fine in development and wrong in production. The list is enforced by the
browser through `frame-ancestors`, not merely checked server-side.

**`trust_proxy_headers`.** Leave it false unless something you control
terminates connections in front of the service and rewrites
`X-Forwarded-For`. Trusting that header from an untrusted source lets any
caller forge their address and walk through every per-address rate limit.

## Running it

A single binary with everything embedded — the widget, the fonts, the locale
bundles. There is nothing else to deploy.

```sh
go build -o captchad ./cmd/captchad
```

Or with Docker, in the Redis arrangement that more than one instance needs:

```sh
docker compose up --build
```

That file carries no demo site. `docker-compose.demo.yml` is the other one:
the service plus the gallery page, on a single instance with the in-memory
store.

**Use Redis as soon as you run more than one instance.** With the in-memory
store, a visitor who starts a challenge on one node and finishes on another
will fail. The Redis driver speaks the protocol directly, so it adds no
dependency; it needs Redis 6.2 or newer for `GETDEL`.

## How it works

1. The page loads `widget.js`, which creates an iframe pointing at the captcha
   service. The challenge therefore runs on the *service's* origin: the host
   page cannot read the artwork, inspect the DOM or synthesise the events that
   solve it.
2. The visitor clicks the checkbox. The widget solves a proof of work and
   posts a set of passive environment signals.
3. The service scores those signals. A clean visitor is usually issued a token
   straight away; a suspicious one, or a sampled share of clean ones, gets a
   puzzle.
4. The puzzle's artwork is generated from a random seed. Only the seed and the
   geometry are stored — the images are redrawn on demand, so a session costs
   about a kilobyte instead of a hundred.
5. The answer is submitted along with the recorded interaction. The service
   grades both. A right answer delivered with machine-like motion is refused.
6. On success the browser receives an opaque 256-bit token, which your backend
   exchanges for a verdict.

[docs/SECURITY.md](docs/SECURITY.md) sets out what this does and does not
defend against, in more detail than is comfortable.

## Languages

`en` and `fa` ship in the box. Persian gets full RTL mirroring, Persian
numerals (`۰۱۲۳`), and its own font stack.

Adding a language means adding `internal/i18n/locales/<tag>.json`. Nothing
enumerates the supported set in code, and no text is ever drawn server-side —
the images contain only shapes, and the browser renders every word — so a new
script needs no font work and no text shaping on the server. A locale missing
any key fails at startup rather than showing a raw key to a visitor.

The Persian font is not committed; see `web/assets/fonts/README.md`, or run:

```sh
./scripts/fetch-fonts.sh
```

## Developing

```
cmd/captchad        the service
cmd/demo            a site that embeds every challenge type, for local work
cmd/preview         dumps sample challenge artwork to PNGs
internal/challenge  generating and grading the four puzzles
internal/imagegen   the procedural renderer: rasterizer, shapes, backgrounds
internal/scoring    behavioural and environment scoring
internal/api        the HTTP surface
web/src             the widget and the iframe application (TypeScript)
packages/react|vue  optional framework wrappers
site                the marketing page, published to GitHub Pages from master
```

Go has no external dependencies at all — not for HTTP, not for YAML, not for
Redis, not for image rendering. For a service whose whole job is to be
difficult to get past, a dependency tree that can be read in an afternoon is
worth more than the code it saves.

```sh
go test ./...                      # the Go suite
cd web && npm install && npm run build   # rebuild the browser bundles
cd web && npm run check            # type-check the TypeScript
```

`web/dist` is **committed on purpose**: `go build` alone produces a working
binary, so a contributor who only touches Go never needs Node. If you change
anything under `web/src`, rebuild and commit the result.

### Updating the browser fixtures

`internal/scoring/testdata/browser` holds interaction traces captured from a
real browser driving the real widget. They are the check that the scorer
agrees with what a browser actually produces, rather than only with the
synthetic traces in the same package. To refresh them, run the service and the
demo, drive the four challenges with a real pointer, and save the `trace`
field from each `POST /v1/solve` body into that directory.

## Licence

MIT.
