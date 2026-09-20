# @persian-captcha/vue

Vue 3 component for the [Persian Captcha](../../README.md) widget.

```sh
npm install @persian-captcha/vue
```

```vue
<script setup lang="ts">
import { ref } from 'vue';
import { PersianCaptcha } from '@persian-captcha/vue';

const captcha = ref();
const token = ref('');
</script>

<template>
  <form @submit.prevent="/* … */">
    <PersianCaptcha
      ref="captcha"
      base-url="https://captcha.example.com"
      sitekey="pc_site_example"
      locale="fa"
      @verify="token = $event"
      @expire="token = ''"
    />
    <button type="submit" :disabled="!token">Sign up</button>
  </form>
</template>
```

| Prop | Required | Meaning |
| --- | --- | --- |
| `sitekey` | yes | Your public site key |
| `base-url` | yes | Base URL of the captcha service |
| `locale` | no | Force a language, e.g. `fa` |
| `theme` | no | `light`, `dark` or `auto` |
| `field-name` | no | Renames the hidden form field |

Events: `verify` (with the token), `expire`, `error` (with a code).
The template ref exposes `reset()`, `getResponse()` and `execute()`.

The component only loads the loader script and forwards events. Everything of
substance happens inside the service's iframe. **The token still has to be
verified on your server** — see [the integration guide](../../docs/INTEGRATION.md).
