# Reaching quince over HTTPS

**quince needs an encrypted origin, and a phone is not `localhost`.** The session cookie is marked
`Secure` for any non-loopback host, so a browser reaching quince over plain `http://` on a LAN
address accepts the login, discards the cookie, and returns you to the login form. Since `qn.6f`
quince refuses that with a `426` naming the cause instead of looping in silence (contracts §1) — but
the refusal is a diagnosis, not a fix. This page is the fix.

There are two supported shapes, and the choice is exactly **does quince terminate TLS**.

---

## Tier 1 — something else terminates TLS (recommended)

quince stays on plain HTTP, reachable only by the proxy, and configures nothing. Onboarding step 1
completes itself: quince sees `X-Forwarded-Proto: https` and asks you for nothing.

### `tailscale serve` — the least work of anything here

No certificate to obtain, renew, or mount. Tailscale terminates TLS with a real cert for your
tailnet name and forwards to quince.

```sh
tailscale serve --bg --https=443 localhost:8968
```

Your quince is then at `https://<machine>.<tailnet>.ts.net/`, and only on the tailnet.

**Check `tailscale serve --help` against your own version before pasting that.** Tailscale's own
documentation records that these flags *"have changed in the 1.52 version"*, and this line is
written to the documented `--https=<port> <target>` form rather than to a shorthand — verified
against Tailscale's CLI reference on 2026-08-02, not recalled.

**This is `tailscale serve`, not `tailscale cert`.** They are different features and the difference
decides which tier you are in: `serve` terminates TLS *for* you (tier 1, nothing to configure in
quince); `cert` writes a certificate and key to disk for *you* to serve (tier 2, below). Reaching
for `cert` when you wanted `serve` is a working setup with more moving parts than it needed.

### A reverse proxy

Caddy, nginx, Traefik — anything that terminates TLS and sets `X-Forwarded-Proto`.

```
# Caddyfile
quince.example.com {
    reverse_proxy 127.0.0.1:8968
}
```

Caddy sets `X-Forwarded-Proto` itself. nginx needs it spelled out:

```nginx
proxy_set_header X-Forwarded-Proto $scheme;
proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;
proxy_set_header Host              $host;
```

**`X-Forwarded-Proto` can only ever upgrade.** quince treats it as evidence the origin is secure and
never as evidence it is not, so a proxy that omits it produces the login loop rather than a silent
downgrade. If step 1 will not complete behind your proxy, that header is the first thing to check.

**`X-Forwarded-For` is different and must be configured.** quince believes it only from addresses
listed in **`QUINCE_TRUSTED_PROXIES`** — a bootstrap environment variable, comma-separated, and
**empty by default**. Out of the box every request is therefore attributed to the proxy's own
address, and the login rate limit is shared by everyone behind it. Set it to your proxy's address
(quince#464).

It is **env rather than `config.yml`**, ruled 2026-08-02 (quince#549), and the reason matters if you
are tempted to move it: `--public-demo` deletes its config at startup, so the deployment that most
needs a trust list could never carry one — and in that mode every visitor can `PUT /api/config`,
which would make a file-based trust list editable by the population it exists to protect against.

**Bind quince to loopback when a proxy is in front.** `QUINCE_LISTEN=127.0.0.1:8968` — otherwise the
plain-HTTP port stays reachable on the LAN and the proxy is a suggestion rather than a boundary.
Note that this conflicts with `network_mode: host` for Wi-Fi backup, which needs quince reachable on
the LAN for mDNS; if you need both, the proxy and quince are on the same host and the LAN-facing
plain port is the trade you are making.

---

## Tier 2 — quince serves TLS with your certificate

Point quince at a certificate and key. It serves HTTPS **on the same port** it already listens on,
and redirects plain HTTP there.

```yaml
tls:
  cert_file: /certs/quince.pem
  key_file:  /certs/quince.key
```

```yaml
# compose
    volumes:
      - /path/to/certs:/certs:ro
```

**Read-only is expected and correct.** quince never writes to that directory — not a renewal, not a
backup copy. Mount it `:ro` and the guarantee is enforced rather than promised.

### One port, both protocols

There is no second port. quince inspects the first byte of each connection — `0x16` is a TLS
handshake, every HTTP request starts with a method letter — and routes accordingly. Plain HTTP gets
a `301` to `https://` at the **same host and port**.

That is deliberate: the URL you onboarded with keeps working, upgraded in place. There is no *"now
go to a different port"*, and no bookmark that starts returning a TLS error — which browsers render
as *"sent an invalid response"* and which is indistinguishable, to a user, from quince being broken.

### Where the certificate comes from

Any of these; quince does not care which.

- **`acme.sh` or `certbot`** in their own container, writing to a shared volume.
- **`tailscale cert <machine>.<tailnet>.ts.net`** — a real, publicly-trusted certificate for your
  tailnet name, written to disk. Use this when you want quince itself to serve TLS on the tailnet;
  use `tailscale serve` (tier 1) if you do not.
- **A wildcard** you already manage. quince serves what it is given and does **not** check that the
  certificate's name matches the host you reached it on, precisely so a wildcard works.

### Rotation needs no restart

quince re-reads the pair when either file changes on disk, so a renewal that rewrites in place is
picked up on the next handshake. No restart, no signal, no reload endpoint.

If a re-read fails — a key half-written mid-renewal, say — quince keeps serving the certificate it
already has and logs a warning. It does not fail the handshake, because a renewal blip should not
take the UI down.

### An unusable certificate stops the process

If `tls:` is set and the pair cannot be loaded, **quince refuses to start**, names the file and the
reason, and exits non-zero. It does not fall back to plain HTTP: that would mean serving the session
cookie in clear to somebody who had configured a certificate, and their browser would not tell them
either.

To go back to plain HTTP, clear **both** keys.

---

## Not recommended, and why they are still here

### Plain HTTP on a network you trust

```yaml
sessions:
  allow_insecure_transport: true
```

Off by default. It relaxes the `Secure` flag for plain-HTTP clients so the login works, and it
**overrides the redirect above** — if you have both a certificate and this flag, plain HTTP is
served rather than upgraded.

**The honest case for it is a VPN.** Over WireGuard or Tailscale the transport is already encrypted;
adding TLS inside the tunnel buys nothing and costs a certificate to manage. In that setting this is
the correct choice rather than the lazy one.

**The cost, stated plainly:** the session cookie and the CSRF token cross the network in clear.
Anyone who can read the path can sign in as you, to an application that shows a person's entire
digital life. On a VPN that path is the tunnel. On a LAN it is everyone on the LAN.

quince says so at startup and in Settings, and will not let you turn the warning off.

**And it forecloses notifications, for the same reason self-signed does.** Browsers only register
service workers — and therefore only allow web push — on a **secure origin**, and plain HTTP to a
LAN address is not one. Note that `http://localhost` *is* a secure context, so a developer testing
locally will not notice this; a phone on the LAN will.

quince does not send push **yet**. Saying so here is the point: this choice decides whether it ever
can. The planned *"your backup is waiting for your passcode"* notification is the answer to Wi-Fi
backup needing an on-device confirmation, and a deployment that picked plain HTTP will find the
feature arrives and does nothing. **Better known while it is still a choice.**

### Self-signed certificates

**quince does not generate one, and this is deliberate rather than unfinished.** A certificate you
click through is not trusted, and Chromium refuses to register service workers on an origin with a
certificate error — the click-through exception does not apply. That would foreclose the push
notifications quince needs for assisted Wi-Fi backup.

You may of course mount a self-signed certificate yourself via tier 2; quince serves what it is
given. You will meet the browser interstitial on every new client, and push will not work.

---

## Which one should I use?

- **On a tailnet** — `tailscale serve`. Nothing to renew, nothing to mount.
- **Already running a reverse proxy** — put quince behind it.
- **Neither, and you have a certificate** — tier 2.
- **Neither, and you are on a VPN** — the plain-HTTP opt-in, knowingly.

The one thing to avoid is plain HTTP on an untrusted network with the opt-in enabled because the
login would not work otherwise. That is the case the `426` exists to name, and the answer is a
certificate rather than the flag.
