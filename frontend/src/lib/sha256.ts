/** SHA-256 的轮常量：前 64 个质数立方根的小数部分 */
const K = Uint32Array.from([
  0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5,
  0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174,
  0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
  0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967,
  0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85,
  0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
  0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3,
  0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208, 0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2,
])

/** 八个 32 位字：工作变量 a..h */
type Words = [number, number, number, number, number, number, number, number]

const rotr = (x: number, n: number) => (x >>> n) | (x << (32 - n))

/**
 * 纯 JS 的 SHA-256（FIPS 180-4），返回 32 字节的摘要。只在没有 WebCrypto 时兜底：
 * crypto.subtle 只在安全上下文（HTTPS、localhost）里有，自托管用 http://局域网 IP 打开时没有
 */
export function sha256(data: Uint8Array): Uint8Array {
  // 填充：0x80，补 0 到长度模 64 余 56，末尾 8 字节是以位计的长度（大端）
  const padded = new Uint8Array(Math.ceil((data.length + 9) / 64) * 64)
  padded.set(data)
  padded[data.length] = 0x80
  const view = new DataView(padded.buffer)
  const bits = data.length * 8
  view.setUint32(padded.length - 8, Math.floor(bits / 2 ** 32))
  view.setUint32(padded.length - 4, bits >>> 0)

  const h = Uint32Array.from([
    0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a, 0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19,
  ])
  const w = new Uint32Array(64)
  for (let offset = 0; offset < padded.length; offset += 64) {
    for (let i = 0; i < 16; i++) w[i] = view.getUint32(offset + i * 4)
    for (let i = 16; i < 64; i++) {
      const a = w[i - 15]!
      const b = w[i - 2]!
      const s0 = rotr(a, 7) ^ rotr(a, 18) ^ (a >>> 3)
      const s1 = rotr(b, 17) ^ rotr(b, 19) ^ (b >>> 10)
      w[i] = w[i - 16]! + s0 + w[i - 7]! + s1
    }
    // 工作变量 a..h 依次放在 s[0..7]
    const s = Uint32Array.from(h)
    for (let i = 0; i < 64; i++) {
      const [a, b, c, d, e, f, g, hh] = s as unknown as Words
      const t1 =
        hh + (rotr(e, 6) ^ rotr(e, 11) ^ rotr(e, 25)) + ((e & f) ^ (~e & g)) + K[i]! + w[i]!
      const t2 = (rotr(a, 2) ^ rotr(a, 13) ^ rotr(a, 22)) + ((a & b) ^ (a & c) ^ (b & c))
      s.set([t1 + t2, a, b, c, d + t1, e, f, g])
    }
    for (let i = 0; i < 8; i++) h[i] = h[i]! + s[i]!
  }
  const out = new Uint8Array(32)
  const outView = new DataView(out.buffer)
  h.forEach((v, i) => outView.setUint32(i * 4, v))
  return out
}
