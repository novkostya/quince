# Getting HTTPS in front of quince

**Your phone is not `localhost`, and that is the whole problem.** Browsers throw away a login
cookie marked `Secure` when the connection is plain `http://`, so you would log in and land straight
back on the login form. quince refuses with a clear message instead of looping — but that is a
diagnosis, not a fix. This page is the fix.

There are two ways round it, and the only question is whether quince handles the certificate itself.

## Let something else handle it

Easiest, and nothing to configure in quince: put a reverse proxy in front — Caddy, nginx, Traefik —
or use a mesh VPN such as Tailscale, which can terminate HTTPS for you with no certificate to
obtain or renew.

```
# Caddyfile
quince.example.com {
    reverse_proxy 127.0.0.1:8968
}
```

Caddy sets the headers itself. nginx needs them spelled out:

```nginx
proxy_set_header X-Forwarded-Proto $scheme;
proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;
proxy_set_header Host              $host;
```

⚠ **`Host` matters more than it looks.** quince derives the domain your passkeys are bound to from
it. Get it wrong and passkeys either fail to register or stop working after they already have —
and nothing in the browser explains why. `proxy_set_header Host $host;` is the whole fix.

You also need to tell quince which proxy to believe, or it ignores those headers and still thinks
you are on plain HTTP. It is an environment variable, not a `config.yml` key:

```yaml
# in compose.yml, under the quince service
    environment:
      QUINCE_TRUSTED_PROXIES: "172.16.0.0/12"
```

Comma-separated, and empty by default — which means trust nothing, so this step is not optional.

## Or let quince do it

Mount your certificate and key where quince can read them:

```yaml
# in compose.yml
    volumes:
      - /path/to/certs:/certs:ro
```

Then open the web UI. quince asks for the two paths on first run, checks the files, and saves
nothing until the certificate has actually worked over https — so a typo or a wrong key leaves you
where you were rather than locked out. There is no config file to edit by hand.

Mount it read-only. quince never writes there, and `:ro` turns that from a promise into a guarantee.

Get the certificate however you like — `certbot` or `acme.sh` in their own container writing to a
shared volume is the usual answer.

**There is no second port.** quince looks at the first byte of each connection and serves HTTPS or
redirects plain HTTP to it, on the same port. The address you set up with keeps working.

**Renewals need no restart.** quince re-reads the files when they change. If a read fails halfway
through a renewal it keeps serving the old certificate and logs it, rather than dropping the UI.

**A broken certificate stops quince starting**, names the file and the reason, and exits. It will
not quietly fall back to plain HTTP — that would put your session cookie in the clear on a system
you had configured for TLS, and your browser would not tell you either.

To go back to plain HTTP, clear the certificate in Settings.

## Plain HTTP, if you really mean it

```yaml
sessions:
  allow_insecure_transport: true
```

**The honest case is a VPN**, where the tunnel is already encrypted and a certificate inside it buys
nothing. That is a reasonable choice, not a lazy one.

**What it costs:** your session cookie crosses the network in the clear, so anyone who can read that
network can sign in as you — to something holding everything on your phone. On a VPN that is the
tunnel. On a LAN it is everyone on the LAN. quince keeps saying so and will not let you dismiss it.

**It also rules out notifications.** Browsers only allow web push on a secure origin, and plain
HTTP to a LAN address is not one. This is not a future cost: quince sends the *"your backup is
waiting for your passcode"* reminder today, and that reminder is the answer to Wi-Fi backups needing
you to confirm on the phone. Choose plain HTTP and you will never get one.

**Self-signed certificates have the same problem.** Chromium will not register a service worker on
an origin with a certificate error, and clicking through the warning does not help. You can mount
one and quince will serve it; you will meet the browser warning on every new device, and push will
never work.

## Which should I pick?

- **Already running a reverse proxy** — put quince behind it.
- **On a mesh VPN that terminates HTTPS** — let it, and configure nothing here.
- **You have a certificate** — hand it to quince.
- **On a VPN, and you would rather not manage a certificate** — plain HTTP, knowingly.

The one to avoid is plain HTTP on a network you do not control, turned on because the login would
not work otherwise. That is the case the refusal exists to name, and the answer is a certificate.
