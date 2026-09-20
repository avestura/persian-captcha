# @persian-captcha/react

React component for the [Persian Captcha](../../README.md) widget.

```sh
npm install @persian-captcha/react
```

```tsx
import { useRef, useState } from 'react';
import { PersianCaptcha, type PersianCaptchaHandle } from '@persian-captcha/react';

function SignUp() {
  const captcha = useRef<PersianCaptchaHandle>(null);
  const [token, setToken] = useState('');

  return (
    <form onSubmit={/* … */ undefined}>
      <PersianCaptcha
        ref={captcha}
        baseUrl="https://captcha.example.com"
        sitekey="pc_site_example"
        locale="fa"
        onVerify={setToken}
        onExpire={() => setToken('')}
      />
      <button type="submit" disabled={!token}>Sign up</button>
    </form>
  );
}
```

| Prop | Required | Meaning |
| --- | --- | --- |
| `sitekey` | yes | Your public site key |
| `baseUrl` | yes | Base URL of the captcha service |
| `locale` | no | Force a language, e.g. `fa` |
| `theme` | no | `light`, `dark` or `auto` |
| `fieldName` | no | Renames the hidden form field |
| `onVerify` | no | Called with the response token |
| `onExpire` | no | Called when the token expires |
| `onError` | no | Called with a machine-readable code |

The ref exposes `reset()`, `getResponse()` and `execute()`.

The component only loads the loader script and forwards callbacks. Everything
of substance happens inside the service's iframe, which is what keeps the
challenge out of reach of the host page. **The token still has to be verified
on your server** — see [the integration guide](../../docs/INTEGRATION.md).

Callbacks are held in refs internally, so passing an inline arrow function
does not tear down a challenge the visitor is part way through.
