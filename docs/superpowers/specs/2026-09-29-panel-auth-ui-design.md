# Panel Auth Hardening & shadcn-Inspired UI Polish — Design

**Date:** 2026-09-29
**Status:** Approved (user request: "poles pakai shadcn, sistem login lebih matang", option B confirmed)

## Goal

Perkuat autentikasi panel (password hash, rate limiting, lockout, session claims, revocation) dan poles UI ke design language shadcn/ui tanpa meninggalkan arsitektur stdlib-only single-file embed.

## Constraint utama

- Panel = Go stdlib-only, single binary, embed satu file HTML. Tidak ada Node/Vite/CDN.
- shadcn/ui literal butuh React build chain → ditolak demi build deterministik.
- Solusi: port token design shadcn (radius, spacing, neutral palette, shadow halus, komponen bersih) ke CSS variables yang ada.

## Auth: kematangan yang ditambah

1. **Password hashing** — plaintext `panel.password` diganti format `pbkdf2$<iter>$<salt-b64>$<hash-b64>` via stdlib `crypto/pbkdf2` (Go 1.24). Backward compat: file plaintext lama di-hash on the fly saat login pertama kali.
2. **Rate limiting + lockout** — per IP: 5 kegagalan → lock 15 menit (in-memory, reset saat restart; cukup untuk panel single-node, `ponytail:` untuk distribusi nanti pakai store cluster).
3. **JWT claims lebih matang** — tambah `iat`, `nbf`, `jti`. Exp tetap 24 jam.
4. **Session revocation** — in-memory denylist `jti` dengan TTL = sisa exp token. Logout memanggil `/api/logout` untuk revoke.
5. **Ganti password** — `handleSettings` ikut menulis hash baru.

## UI: polish shadcn-style

- Login: card centered, logo ring halus, input besar, focus ring ala shadcn, show/hide password, error inline, disabled saat submit.
- Palette: neutral zinc/slate, radius 10px/0.5rem, border subtle, shadow-sm, transitions lembut.
- Tetap satu file, tidak ada framework JS baru.

## Testing

- TDD: test Go untuk hashing, lockout, rate limit, revocation, backward-compat plaintext.
- E2E: real binary login flow (file hash, lockout, revoke) via httptest + subprocess.
