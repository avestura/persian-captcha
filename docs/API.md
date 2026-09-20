# HTTP API

Two audiences, and the split matters.

**Browser routes** are called from the service's own iframe. They are
same-origin, carry no credentials and need no CORS. Nothing else should call
them; the widget is the only supported client, and the protocol may change
between versions.

**Backend routes** — really just `/v1/siteverify` — are called server to
server with a secret key. That one is stable and is what an integration
depends on.

Every reply is JSON, `Cache-Control: no-store`, UTF-8.

---

## Backend

### `POST /v1/siteverify`

Exchanges a captcha response token for a verdict, consuming it.

Accepts `application/x-www-form-urlencoded` (the shape every reCAPTCHA client
already sends) or `application/json`.

**Request**

| Field | Required | Meaning |
| --- | --- | --- |
| `secret` | yes | The site's secret key |
| `response` | yes | The token from the browser |
| `remoteip` | no | The visitor's address; recorded by the caller, not used for the verdict |

**Reply** — always `200` when the request itself was well-formed; the outcome
is in the body.

```json
{
  "success": true,
  "challenge_ts": "2026-09-19T22:22:45Z",
  "hostname": "app.example.com",
  "score": 0.94,
  "solved": "slider_jigsaw"
}
```

| Field | Type | Meaning |
| --- | --- | --- |
| `success` | bool | The token was valid, unspent and belongs to this site |
| `challenge_ts` | string | Issue time, RFC 3339 |
| `hostname` | string | Host the captcha was solved on |
| `score` | number | Human-likeness, 0 to 1, rounded to two decimals |
| `solved` | string | `slider_jigsaw`, `rotate`, `click_order`, `drag_drop`, `accessible`, or `passive` |
| `error-codes` | array | Present when `success` is false |

Error codes: `missing-input-secret`, `invalid-input-secret`,
`missing-input-response`, `invalid-input-response`, `timeout-or-duplicate`,
`bad-request`, `internal-error`.

`timeout-or-duplicate` covers "never existed", "expired" and "already used"
with one code on purpose. Distinguishing them would tell a caller which of
their guesses were real tokens.

Ownership is checked before the token is spent, so presenting someone else's
token with your secret fails without destroying theirs.

---

## Browser

The order is fixed: `session` → `assess` → (`challenge`) → `solve`.

### `POST /v1/session`

Opens a session and issues the first proof of work.

```json
{ "sitekey": "pc_site_example", "origin": "https://app.example.com", "locale": "fa" }
```

```json
{
  "sid": "…43 characters…",
  "pow": { "alg": "sha256-lz", "prefix": "9f2c…", "bits": 16 },
  "locale": "fa",
  "ttl": 300
}
```

`403 origin` is returned both for an unknown site key and for a disallowed
origin, with an identical body, so the endpoint cannot be used to enumerate
valid keys.

### `POST /v1/assess`

Submits the proof of work and the passive environment signals, and receives a
verdict.

```json
{
  "sid": "…",
  "nonce": "48219",
  "signals": {
    "webdriver": false, "touch": false, "cores": 8, "dpr": 2,
    "sw": 2560, "sh": 1440, "vw": 1280, "vh": 900,
    "langs": 2, "moves": 55, "dwell": 3100
  }
}
```

Replies with a [flow response](#flow-response), whose `status` is `pass`,
`challenge` or `blocked`.

### `POST /v1/challenge`

Asks for a different puzzle. `kind` may be `accessible` to switch to the
non-visual question; any other value rerolls the visual puzzle.

```json
{ "sid": "…", "kind": "accessible" }
```

Rerolling is capped per session, so a solver cannot shop for an easy board.
Switching to the accessible question is **never** rationed: a visitor who
needs it may have to reach it after several attempts.

### `POST /v1/solve`

Submits an answer together with the recorded interaction.

```json
{
  "sid": "…",
  "nonce": "18822",
  "answer": { "x": 188 },
  "trace": {
    "mode": "pointer",
    "points": [{ "x": 19.07, "y": 226.91, "t": 2226 }],
    "startT": 2226, "endT": 3374, "corrections": 1
  }
}
```

Answer shapes, one per challenge kind:

| Kind | Answer |
| --- | --- |
| `slider_jigsaw` | `{"x": number}` — the piece image's left edge |
| `rotate` | `{"angle": number}` — total clockwise correction, degrees |
| `click_order` | `{"points": [{"x","y"}, …]}` — in the order clicked |
| `drag_drop` | `{"placements": [{"id","x","y"}, …]}` — each piece's top-left |
| `accessible` | `{"choice": "2"}` — the chosen option's id |

`trace.mode` is `pointer`, `touch` or `keyboard`. Keyboard input is scored on
a separate path, because arrow keys legitimately produce the even spacing that
would look mechanical from a mouse.

A correct answer delivered with implausible motion is **not** accepted. That
is the point of the challenge: computing the right offset is easy, producing a
convincing gesture is not.

### `GET /v1/c/{sid}/{part}`

Renders one image of the challenge in flight. `part` is `board`, `piece`,
`disc` or `pieceN`, as listed in the `assets` map of the flow response.

Returns `image/png`, `no-store`. Bound to the session and the client that
opened it; anything else gets `404`.

The image is not stored anywhere. It is redrawn from the session's seed on
each request, which is why a session costs about a kilobyte rather than a
hundred.

### Flow response

Returned by `assess`, `challenge` and `solve`.

```json
{
  "status": "challenge",
  "challenge": { "kind": "slider_jigsaw", "width": 320, "height": 200,
                 "slider": { "pieceSize": 84, "pieceTop": 44, "travelMax": 236 } },
  "assets": { "board": "/v1/c/…/board?v=48211", "piece": "/v1/c/…/piece?v=48211" },
  "pow": { "alg": "sha256-lz", "prefix": "1a3f…", "bits": 17 },
  "attemptsLeft": 3,
  "canFallBack": true
}
```

| `status` | Means |
| --- | --- |
| `pass` | Verified. `token` and `expiresIn` are set. |
| `challenge` | Solve the puzzle described in `challenge`. |
| `retry` | The last answer was wrong; a fresh puzzle is attached. |
| `blocked` | This session is over. |

The `challenge` object never contains the answer: no target offset, no
rotation, no icon positions, no correct option. Those live only in the session
store. A `pass` carries no challenge, and a `blocked` carries neither.

Each challenge comes with its own proof of work, which must be solved before
that challenge's answer is accepted. The difficulty rises with each failed
attempt, so grinding through retries costs progressively more.

---

## Assets and health

| Route | Serves |
| --- | --- |
| `GET /v1/widget.js` | The host-page loader. Cached 10 minutes, ETagged. |
| `GET /v1/frame` | The challenge document, with `frame-ancestors` set from the site's allow-list. |
| `GET /v1/frame.js`, `/v1/frame.css` | The iframe application. |
| `GET /v1/pow.js` | The proof-of-work worker. Same-origin so the policy can keep `worker-src 'self'`. |
| `GET /v1/font/{name}` | A bundled webfont, if the operator installed one. |
| `GET /v1/locales` | The available languages. |
| `GET /v1/locale/{tag}` | One message bundle, for switching language without reloading. |
| `GET /healthz` | Liveness. Always `{"status":"ok"}` if the process is up. |
| `GET /readyz` | Readiness. Pings the store; `503` when it is unreachable. |

Use `/healthz` for liveness and `/readyz` for readiness. They differ: a
service whose Redis has gone away is unhealthy but should not be restarted,
because restarting will not bring Redis back.

## Rate limits

Fixed one-minute windows, counted per hashed client address, configurable per
deployment:

| Bucket | Default |
| --- | --- |
| `/v1/session` | 30 per minute |
| `/v1/assess`, `/v1/solve`, `/v1/challenge` | 90 per minute each |
| Challenge images | 4× the solve limit |
| `/v1/siteverify` | 1200 per minute per site |

Exceeding one gives `429` with `Retry-After`. A store outage fails **closed**:
if the limiter cannot be consulted the request is refused, because an open
door is worse than a brief outage.
