/**
 * Proof-of-work worker.
 *
 * Searches for a nonce N such that SHA-256("<prefix>.<N>") begins with the
 * requested number of zero bits. It runs in a worker because a typical target
 * is a few hundred thousand hashes: on the main thread that would lock up the
 * host page for a noticeable fraction of a second.
 *
 * SHA-256 is implemented here rather than via crypto.subtle because that API
 * is promise-based, and the per-call overhead of awaiting hundreds of
 * thousands of promises dwarfs the cost of the hash itself. A synchronous
 * implementation over a single 64-byte block is roughly two orders of
 * magnitude faster for this workload.
 */

const K = new Uint32Array([
  0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5,
  0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174,
  0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
  0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967,
  0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85,
  0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
  0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3,
  0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208, 0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2,
]);

/** Reused across iterations so the search allocates nothing in its hot loop. */
const W = new Uint32Array(64);
const block = new Uint8Array(64);

/**
 * Hashes a single padded 64-byte block and returns the first two words of the
 * digest, which is all the leading-zero test can need.
 */
function sha256Block(): [number, number] {
  for (let i = 0; i < 16; i++) {
    const j = i * 4;
    W[i] = ((block[j] << 24) | (block[j + 1] << 16) | (block[j + 2] << 8) | block[j + 3]) >>> 0;
  }
  for (let i = 16; i < 64; i++) {
    const w15 = W[i - 15];
    const w2 = W[i - 2];
    const s0 = ((w15 >>> 7) | (w15 << 25)) ^ ((w15 >>> 18) | (w15 << 14)) ^ (w15 >>> 3);
    const s1 = ((w2 >>> 17) | (w2 << 15)) ^ ((w2 >>> 19) | (w2 << 13)) ^ (w2 >>> 10);
    W[i] = (W[i - 16] + s0 + W[i - 7] + s1) >>> 0;
  }

  let a = 0x6a09e667;
  let b = 0xbb67ae85;
  let c = 0x3c6ef372;
  let d = 0xa54ff53a;
  let e = 0x510e527f;
  let f = 0x9b05688c;
  let g = 0x1f83d9ab;
  let h = 0x5be0cd19;

  for (let i = 0; i < 64; i++) {
    const S1 = ((e >>> 6) | (e << 26)) ^ ((e >>> 11) | (e << 21)) ^ ((e >>> 25) | (e << 7));
    const ch = (e & f) ^ (~e & g);
    const t1 = (h + S1 + ch + K[i] + W[i]) >>> 0;
    const S0 = ((a >>> 2) | (a << 30)) ^ ((a >>> 13) | (a << 19)) ^ ((a >>> 22) | (a << 10));
    const maj = (a & b) ^ (a & c) ^ (b & c);
    const t2 = (S0 + maj) >>> 0;

    h = g;
    g = f;
    f = e;
    e = (d + t1) >>> 0;
    d = c;
    c = b;
    b = a;
    a = (t1 + t2) >>> 0;
  }

  return [(a + 0x6a09e667) >>> 0, (b + 0xbb67ae85) >>> 0];
}

/** Writes an ASCII string into the block buffer and applies SHA-256 padding. */
function prepare(text: string): boolean {
  const len = text.length;
  // Only single-block messages are supported: the length byte and the 0x80
  // terminator need nine bytes, so 55 characters is the ceiling. Prefixes are
  // 24 characters and nonces are short decimal numbers, so this always holds.
  if (len > 55) return false;
  for (let i = 0; i < len; i++) {
    block[i] = text.charCodeAt(i) & 0xff;
  }
  block[len] = 0x80;
  for (let i = len + 1; i < 56; i++) block[i] = 0;
  const bits = len * 8;
  block[56] = 0;
  block[57] = 0;
  block[58] = 0;
  block[59] = 0;
  block[60] = (bits >>> 24) & 0xff;
  block[61] = (bits >>> 16) & 0xff;
  block[62] = (bits >>> 8) & 0xff;
  block[63] = bits & 0xff;
  return true;
}

function leadingZeroBits(h0: number, h1: number): number {
  if (h0 !== 0) return Math.clz32(h0);
  if (h1 !== 0) return 32 + Math.clz32(h1);
  return 64;
}

interface Task {
  prefix: string;
  bits: number;
  alg: string;
}

/** Above this the search is abandoned rather than spinning indefinitely. */
const MAX_ITERATIONS = 80_000_000;

self.onmessage = (event: MessageEvent) => {
  const task = event.data as Task;
  if (!task || task.alg !== 'sha256-lz') {
    (self as unknown as Worker).postMessage({ error: 'unsupported' });
    return;
  }
  const target = Math.max(1, Math.min(32, task.bits | 0));
  const head = task.prefix + '.';

  for (let n = 0; n < MAX_ITERATIONS; n++) {
    if (!prepare(head + n)) {
      (self as unknown as Worker).postMessage({ error: 'unsupported' });
      return;
    }
    const [h0, h1] = sha256Block();
    if (leadingZeroBits(h0, h1) >= target) {
      (self as unknown as Worker).postMessage({ nonce: String(n) });
      return;
    }
  }
  (self as unknown as Worker).postMessage({ error: 'pow_exhausted' });
};
