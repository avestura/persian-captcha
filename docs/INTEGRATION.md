# Integrating Persian Captcha

Two halves, and both are required. The widget on the page produces a token;
your backend exchanges that token for a verdict. Skipping the second half
means the captcha does nothing at all, because the browser is free to claim
whatever it likes.

- [1. Get your keys](#1-get-your-keys)
- [2. Put the widget on the page](#2-put-the-widget-on-the-page)
- [3. Verify on your server](#3-verify-on-your-server)
- [Framework wrappers](#framework-wrappers)
- [Invisible mode](#invisible-mode)
- [Choosing a language](#choosing-a-language)
- [Handling failure](#handling-failure)
- [Common mistakes](#common-mistakes)

## 1. Get your keys

Whoever runs the captcha service adds your site to its configuration and gives
you two strings:

| | Where it goes | Who may see it |
| --- | --- | --- |
| **Site key** (`pc_site_…`) | The page, in HTML | Everyone. It is public. |
| **Secret key** (`pc_secret_…`) | Your server, in an environment variable | Only your server. |

The site key is useless without a matching origin: it only works on the
origins the operator listed for it, enforced by the browser. The secret key is
what lets you spend tokens, so treat it like a password and never let it reach
a browser.

## 2. Put the widget on the page

The simplest integration is one script tag and one div:

```html
<form method="post" action="/signup">
  <label>Email <input name="email" type="email" required></label>

  <div class="pcaptcha" data-sitekey="pc_site_example"></div>

  <button type="submit">Sign up</button>
</form>

<script src="https://captcha.example.com/v1/widget.js" async defer></script>
```

The script finds every `.pcaptcha` element, renders a widget into it, and adds
a hidden `pcaptcha-response` input to the enclosing form. When the form is
submitted normally, the token travels with it.

### Attributes

| Attribute | Purpose |
| --- | --- |
| `data-sitekey` | **Required.** Your public site key. |
| `data-locale` | Force a language, e.g. `fa`. Otherwise the site's configured default applies. |
| `data-theme` | `light`, `dark` or `auto` (default). |
| `data-field-name` | Rename the hidden field from `pcaptcha-response`. |
| `data-callback` | Name of a global function called with the token. |
| `data-expired-callback` | Name of a global function called when the token expires. |
| `data-error-callback` | Name of a global function called with an error code. |

### The JavaScript API

For anything more involved, render explicitly:

```html
<script src="https://captcha.example.com/v1/widget.js?render=explicit&onload=setupCaptcha"></script>
<script>
  let widgetId;
  function setupCaptcha() {
    widgetId = PersianCaptcha.render(document.getElementById('captcha-here'), {
      sitekey: 'pc_site_example',
      locale: 'fa',
      theme: 'dark',
      callback: (token) => { console.log('verified'); },
      'expired-callback': () => { console.log('token expired'); },
      'error-callback': (code) => { console.log('failed:', code); },
    });
  }
</script>
```

| Method | Does |
| --- | --- |
| `PersianCaptcha.render(el, options)` | Renders a widget, returns its id |
| `PersianCaptcha.getResponse(id?)` | The current token, or `''` |
| `PersianCaptcha.reset(id?)` | Clears it; the visitor must verify again |
| `PersianCaptcha.execute(id?)` | Starts verification programmatically |
| `PersianCaptcha.remove(id)` | Removes the widget entirely |

With the id omitted, `reset` and `execute` apply to every widget on the page,
and `getResponse` returns the first one's token.

`window.pcaptcha` is an alias for `window.PersianCaptcha`.

### For a form posted with fetch

```js
const form = document.querySelector('form');
form.addEventListener('submit', async (e) => {
  e.preventDefault();

  const token = PersianCaptcha.getResponse();
  if (!token) return alert('Please complete the verification first.');

  await fetch('/signup', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ email: form.email.value, captcha: token }),
  });

  // A token can only be spent once, so reset before the form can be sent
  // again.
  PersianCaptcha.reset();
});
```

## 3. Verify on your server

Exchange the token for a verdict before you act on the request. The endpoint
accepts form encoding (what every reCAPTCHA and hCaptcha client already
sends) or JSON.

**Request** — `POST https://captcha.example.com/v1/siteverify`

| Field | Required | Meaning |
| --- | --- | --- |
| `secret` | yes | Your secret key |
| `response` | yes | The token from the browser |
| `remoteip` | no | The visitor's address, for your own logs |

**Reply**

```json
{
  "success": true,
  "challenge_ts": "2026-09-19T22:22:45Z",
  "hostname": "app.example.com",
  "score": 0.94,
  "solved": "slider_jigsaw"
}
```

| Field | Meaning |
| --- | --- |
| `success` | Whether the token was valid and unspent |
| `challenge_ts` | When it was issued (RFC 3339) |
| `hostname` | The site it was earned on |
| `score` | Human-likeness, 0 to 1. Higher is more human. |
| `solved` | The challenge kind, or `passive` if no puzzle was needed |
| `error-codes` | Present when `success` is false |

| Error code | Means |
| --- | --- |
| `missing-input-secret` | No secret was sent |
| `invalid-input-secret` | The secret is not one this service knows |
| `missing-input-response` | No token was sent — usually a form-parsing bug |
| `invalid-input-response` | The token belongs to a different site |
| `timeout-or-duplicate` | Unknown, expired, or already spent |
| `bad-request` | The request itself was malformed |

### Go

```go
type verdict struct {
	Success    bool     `json:"success"`
	Score      float64  `json:"score"`
	Hostname   string   `json:"hostname"`
	Solved     string   `json:"solved"`
	ErrorCodes []string `json:"error-codes"`
}

func verifyCaptcha(ctx context.Context, token, remoteIP string) (*verdict, error) {
	form := url.Values{
		"secret":   {os.Getenv("CAPTCHA_SECRET")},
		"response": {token},
		"remoteip": {remoteIP},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://captcha.example.com/v1/siteverify", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	res, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	var out verdict
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}
```

### Node

```js
async function verifyCaptcha(token, remoteip) {
  const res = await fetch('https://captcha.example.com/v1/siteverify', {
    method: 'POST',
    headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
    body: new URLSearchParams({
      secret: process.env.CAPTCHA_SECRET,
      response: token,
      remoteip,
    }),
    signal: AbortSignal.timeout(10_000),
  });
  return res.json();
}

app.post('/signup', async (req, res) => {
  const verdict = await verifyCaptcha(req.body['pcaptcha-response'], req.ip);
  if (!verdict.success) {
    return res.status(400).json({ error: 'captcha failed', codes: verdict['error-codes'] });
  }
  // …proceed
});
```

### Python

```python
import os, requests

def verify_captcha(token: str, remote_ip: str | None = None) -> dict:
    return requests.post(
        "https://captcha.example.com/v1/siteverify",
        data={
            "secret": os.environ["CAPTCHA_SECRET"],
            "response": token,
            "remoteip": remote_ip or "",
        },
        timeout=10,
    ).json()


@app.post("/signup")
def signup():
    verdict = verify_captcha(request.form.get("pcaptcha-response"), request.remote_addr)
    if not verdict["success"]:
        abort(400, "captcha failed")
    # …proceed
```

### PHP

```php
$verdict = json_decode(file_get_contents(
    'https://captcha.example.com/v1/siteverify',
    false,
    stream_context_create(['http' => [
        'method'  => 'POST',
        'header'  => "Content-Type: application/x-www-form-urlencoded\r\n",
        'timeout' => 10,
        'content' => http_build_query([
            'secret'   => getenv('CAPTCHA_SECRET'),
            'response' => $_POST['pcaptcha-response'] ?? '',
            'remoteip' => $_SERVER['REMOTE_ADDR'],
        ]),
    ]])
), true);

if (empty($verdict['success'])) {
    http_response_code(400);
    exit('captcha failed');
}
```

### Using the score

`score` runs from 0 to 1, where 1 is most human. It is a hint, not a verdict:
`success` already means the visitor solved the challenge. Use the score to
decide how much *additional* friction a borderline request deserves — hold a
comment for moderation, ask for email confirmation — rather than to reject
outright. Anything above about 0.6 is an ordinary visitor.

## Framework wrappers

Thin components that do nothing but load the script and forward callbacks.
Everything of substance stays inside the service's iframe.

### React

```tsx
import { useRef } from 'react';
import { PersianCaptcha, type PersianCaptchaHandle } from '@persian-captcha/react';

function SignUp() {
  const captcha = useRef<PersianCaptchaHandle>(null);
  const [token, setToken] = useState('');

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    await fetch('/signup', { method: 'POST', body: JSON.stringify({ captcha: token }) });
    captcha.current?.reset();   // the token is spent
    setToken('');
  }

  return (
    <form onSubmit={submit}>
      <PersianCaptcha
        ref={captcha}
        baseUrl="https://captcha.example.com"
        sitekey="pc_site_example"
        locale="fa"
        onVerify={setToken}
        onExpire={() => setToken('')}
        onError={(code) => console.error('captcha:', code)}
      />
      <button type="submit" disabled={!token}>Sign up</button>
    </form>
  );
}
```

### Vue

```vue
<script setup lang="ts">
import { ref } from 'vue';
import { PersianCaptcha } from '@persian-captcha/vue';

const captcha = ref();
const token = ref('');

async function submit() {
  await fetch('/signup', { method: 'POST', body: JSON.stringify({ captcha: token.value }) });
  captcha.value?.reset();
  token.value = '';
}
</script>

<template>
  <form @submit.prevent="submit">
    <PersianCaptcha
      ref="captcha"
      base-url="https://captcha.example.com"
      sitekey="pc_site_example"
      locale="fa"
      @verify="token = $event"
      @expire="token = ''"
      @error="(code) => console.error('captcha:', code)"
    />
    <button type="submit" :disabled="!token">Sign up</button>
  </form>
</template>
```

Both are in `packages/`. Build them with `npm install && npm run build` there.

## Invisible mode

To verify at submit time rather than showing a checkbox up front, render the
widget somewhere out of the way and call `execute()`:

```js
form.addEventListener('submit', (e) => {
  if (PersianCaptcha.getResponse()) return;   // already verified, let it through
  e.preventDefault();
  PersianCaptcha.execute();
});

function onVerified() {
  form.submit();   // the callback named in data-callback
}
```

The visitor sees nothing unless a puzzle is warranted, in which case it opens
as an overlay.

## Choosing a language

In order of precedence:

1. `data-locale` on the element, or `locale` in the render options.
2. The site's configured default, set by whoever runs the service.
3. The browser's `Accept-Language`.
4. The service default, normally `en`.

The site default deliberately outranks the browser. A Persian-language site
wants the widget in Persian even for a visitor whose browser is set to
English, because the widget sits inside their page and should match it. The
widget also offers its own language picker, so a visitor who cannot read the
chosen language is never stuck.

## Handling failure

The error callback receives a code:

| Code | Means | What to do |
| --- | --- | --- |
| `blocked` | Too many failed attempts | Ask them to wait a few minutes |
| `session` | The challenge expired | The widget resets itself |
| `rate_limited` | Too many requests from this address | Back off |
| `origin` | This site key is not valid here | A configuration error — check `allowed_origins` |
| `network` | The service could not be reached | Retry, or fall back |
| `script_load_failed` | `widget.js` did not load | Do not silently block the form |

Decide in advance what happens when the captcha service is unreachable. Both
answers are defensible — refuse the request, or let it through and flag it —
but the one you must avoid is a form that cannot be submitted and does not say
why.

## Common mistakes

**Trusting the browser.** A token that has not been through `/v1/siteverify`
proves nothing. Anyone can post your form directly with any string they like.

**Reusing a token.** Tokens are single use, deliberately. After a successful
verification, reset the widget before the form can be sent again.

**Parsing the form with the wrong function.** If your page posts a `FormData`
object, the browser encodes it as multipart. In Go, `r.ParseForm` reads only
urlencoded bodies and will silently leave the token empty — use
`r.FormValue`. This one presents as `missing-input-response` and costs people
an afternoon.

**Leaking the secret key.** It belongs in an environment variable on your
server. If it reaches a browser, anyone can spend your tokens.

**Leaving `allowed_origins` empty in production.** Any site could then embed
your key and farm tokens against your quota.

**Forgetting the token expires.** It stays redeemable for a few minutes. A
long form that sits open needs the `expired-callback` handled, or the
submission will fail at the last step.
