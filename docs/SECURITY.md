# Security model

What this service defends against, how, and — more usefully — what it does
not. If you are deciding whether to deploy it, the last section is the one to
read.

## What a captcha can actually do

No captcha proves a visitor is human. Every interactive captcha in existence
can be solved by a person paid a fraction of a cent, and most can be solved by
a model for less. What a captcha does is make abuse *cost* something, so that
attacks which only pay off at scale stop paying off.

Everything below is aimed at that: raising the per-solve cost in CPU, in
engineering effort, and in the difficulty of doing it a million times.

## The layers

### 1. Origin isolation

The challenge runs in an iframe served from the captcha service's own origin,
never inline in the host page. The host page cannot read the artwork, walk the
challenge DOM, or dispatch the events that solve it. A site that embeds the
widget is not in a position to solve its own captchas, which matters when the
site itself may be compromised.

The iframe carries a Content-Security-Policy with `frame-ancestors` built from
the site's configured origin allow-list. This is enforced by the browser, not
merely checked by the server, which is what makes the allow-list meaningful:
an unlisted site cannot embed the widget at all, whatever headers it sends.

The policy also sets `default-src 'none'`, a nonce for the only two scripts,
`worker-src 'self'` and no `unsafe-eval`.

### 2. Proof of work

Before a challenge is issued, and again before each answer is accepted, the
browser must find a nonce whose SHA-256 has a given number of leading zero
bits. Sixteen bits is roughly 65,000 hashes: tens of milliseconds in a worker,
unnoticeable to a visitor.

Multiplied by a farm's throughput, it is not unnoticeable. A million solves is
a million CPU-seconds on top of everything else, and the difficulty rises with
each failed attempt and with the session's risk score, so grinding is
progressively more expensive than succeeding.

It is not a defence against a single determined attacker. It is a defence
against volume.

### 3. Behavioural scoring

This is the part that earns the interactive challenge its place.

A script can compute the correct slider offset from the image far more
reliably than a person can find it by eye. Grading the answer alone would stop
nobody. What is hard to fake convincingly is the *delivery*: real pointer paths
wobble off-axis, overshoot and correct, decelerate on approach, and arrive at
the browser's jittery frame clock. Synthetic ones tend to be straight, evenly
sampled, whole-pixel and constant-speed.

The scorer looks at path straightness, lateral deviation, the coefficient of
variation of inter-sample times, the speed profile, the deceleration on
approach, sub-pixel coordinates and step quantisation. Each signal alone is
weak; together they separate a hand from a naive script cleanly.

A correct answer with a machine-like trace is refused. That rejection is
logged distinctly, because a rise in its rate is the clearest sign that
somebody is actively working on you.

Keyboard input is scored on a separate path. Arrow keys legitimately produce
the even spacing that would be damning from a mouse, and applying the pointer
rules to them would lock out every visitor who cannot use one.

### 4. Environment signals

A small set of passive properties: `navigator.webdriver`, whether the viewport
fits inside the screen, whether any language is declared, whether the pointer
ever moved before the click. Weakly weighted, because plenty of real people
browse with unusual settings and being unusual is not being a robot.

These are cheap to fake, which is exactly why they cannot be trusted on their
own — see *sampling*, below.

### 5. Sampling

A clean-looking visitor is shown a puzzle anyway, some fraction of the time
(`challenge_rate`, 25% by default).

Without this, a headless browser reporting a convincing environment would
sail through every single time and the interactive defence would never fire.
Sampling means an attacker cannot rely on never meeting a puzzle, and it keeps
the interactive path exercised by real traffic rather than only by tests.

### 6. Single-use tokens

A successful solve yields an opaque 256-bit random token, stored server-side
and redeemed exactly once. Redemption is atomic, so two concurrent
verifications of the same token cannot both succeed.

Tokens are bound to the site that earned them, and ownership is checked
*before* the token is spent, so a caller presenting someone else's token with
their own secret fails without destroying it.

The session is deleted the moment a token is issued, so one session can never
mint a second.

### 7. Sessions bound to their client

A session is tied to a hash of the address that opened it. A session id
captured in transit is useless from anywhere else.

This depends on the service knowing the real address, which depends on
`trust_proxy_headers` being set correctly. See below.

### 8. Rate limits

Fixed one-minute windows per hashed address, on session creation, on solving,
on image rendering and on verification. Failures are counted separately and
more strictly than ordinary traffic.

Limits fail **closed**: if the store cannot be reached, requests are refused
rather than waved through.

### 9. Unguessable, unstored artwork

Backgrounds and puzzle pieces are generated procedurally from a
cryptographically random 64-bit seed, so there is no fixed image set to
scrape, hash and precompute answers for. Only the seed and the geometry are
stored; images are redrawn on demand and never cached, by the server or the
browser.

The jigsaw piece silhouette is randomised per challenge, so a template match
against a known piece shape does not generalise.

## Privacy

Deliberately minimal, because a captcha that requires a privacy policy is a
captcha that is hard to deploy.

* **No cookies.** Nothing is stored in the browser.
* **No fingerprinting.** No canvas, WebGL or audio fingerprinting, no font
  enumeration, no attempt at a cross-site identifier.
* **No stored addresses.** Rate limiting needs to recognise a repeat visitor,
  not identify them. Addresses are hashed with a secret generated fresh at
  process start, so the stored value is useless to anyone who reads the
  session store and becomes meaningless on restart.
* **Nothing outlives the session.** Signals and traces are scored, used, and
  discarded with the session minutes later. Nothing is written to a log that
  could reconstruct a visitor.
* **No third parties.** The service talks to nothing but its own store.

## Operational requirements

Three settings decide whether the model above holds.

**`allowed_origins` must be populated.** An empty list means any site may
embed that key. That is convenient in development and wrong in production: any
site could embed your key and farm tokens against your quota.

**`trust_proxy_headers` must match reality.** Set it true only when something
you control terminates connections in front of the service and rewrites
`X-Forwarded-For`. Set it true with the service directly exposed, and any
caller can forge their address, which defeats every per-address rate limit and
the session binding with it. Set it false behind a proxy, and every visitor
appears to come from the proxy, so the limits apply to all of them together.

**Secret keys must stay on servers.** The site key is public by design. The
secret key is what spends tokens. If it reaches a browser, anyone can verify
anything.

## What this does not defend against

Stated plainly, because a security document that only lists strengths is
marketing.

**Human solving farms.** A person paid to solve captchas produces genuine
human motion, because it is genuine human motion. Nothing here detects that,
and nothing can. Proof of work and rate limits raise the cost; they do not
close the door.

**A determined, well-engineered attacker.** Someone willing to drive a real
browser, generate plausible mouse paths and pay the proof of work will get
through. The defence is that doing so is considerably more work than
`requests.post()`, and that the effort does not transfer to the next service
they meet.

**Vision models reading the board.** A capable model can locate a notch or
name an icon. This is why the answer is not the whole test: the motion is
graded too, and a model that solves the geometry still has to deliver it
convincingly.

**A compromised host page.** A site whose JavaScript is under an attacker's
control can wait for a legitimate visitor to solve a captcha and then use the
token for something else. The iframe stops the page solving the captcha
itself; it cannot stop the page misusing a token the visitor legitimately
earned. That is a property of every captcha, and the reason tokens are
short-lived and single-use.

**Denial of service.** Rate limits cap per-address throughput, and rendering
is the most expensive operation the service performs. A distributed flood
still needs a layer in front.

**Abuse that does not need scale.** A captcha is the wrong tool against a
targeted attack on one account. Use rate limits, second factors and anomaly
detection for that.

## Reporting a vulnerability

Report privately to the maintainers rather than opening a public issue, and
please include enough detail to reproduce. Findings in the scoring heuristics
are welcome and expected — they are heuristics, and the interesting question
is always which real visitors they misjudge.
