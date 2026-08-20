# The ZFS helper script

quince runs in a container and ZFS lives on your host. Rather than give the container ZFS
privileges, quince connects over SSH to a small script that does only the few things it needs.

You install that script once per ZFS host. This page is how.

## Get the script

The *Add storage* screen hands you the finished file with a copy button — nothing in it needs
editing. It is also in the repository at `core/internal/storage/zfshelper/quince-zfs-helper`,
and every install of a given version gets identical bytes.

## Install it on the ZFS host

```sh
# as root on the ZFS host
install -m 755 quince-zfs-helper /usr/local/sbin/quince-zfs-helper
```

Then add a **dedicated** SSH key for quince, and force the script as that key's only command:

```
command="/usr/local/sbin/quince-zfs-helper rpool/quince",no-port-forwarding,no-agent-forwarding,no-pty,no-X11-forwarding ssh-ed25519 AAAA... quince
```

**Your parent dataset goes after the script path, inside the same pair of quotes.** `sshd` reads
`command="…"` as one value — close the quote after the path and the dataset lands where `sshd`
expects the next option, and it rejects the whole line.

Passing the dataset here, rather than inside the script, is what confines quince: the script reads
it before it looks at anything the client asked for.

## The key

quince generates one key per parent dataset when you press the button on the *Add storage*
screen, at `/data/keys/zfs-<dataset>` with `/` written as `+` — `tank/quince` becomes
`/data/keys/zfs-tank+quince`. Two storages under one parent share a key, because they have
identical access anyway. Set `ssh_key` yourself if you already have a key deployed.

## Point quince at it

```yaml
zfs:
  parent_dataset: rpool/quince
  ssh_user: zfsuser
  ssh_host: zfshost
  # ssh_port: 22               # both optional — these are the defaults
  # ssh_key: /data/keys/zfs    # unless your key lives elsewhere
```

## Trust the host key — this step is not optional

**Skip it and every call fails, from the first one.** quince connects non-interactively, so SSH
cannot show you the *"are you sure you want to continue connecting?"* prompt — it refuses instead.
A fresh install has an empty `known_hosts`, which is exactly when you are setting this up. What
you see is:

```
Test helper → outcome: unreachable
              detail:  Host key verification failed.
```

Nothing about the key, the script or the pool is wrong when that appears.

Pin the key once, on the host running quince:

```sh
ssh-keyscan -t ed25519 zfshost >> /path/to/quince/data/keys/known_hosts   # then read it before trusting it
```

quince connects with `StrictHostKeyChecking=yes` — not `accept-new`, which would trust whatever
answers first, on the one connection where that matters. Pinning the key yourself is what stands
between your backups and somebody else's host.

## What the script can and cannot do

Worth knowing before you install something as root:

- it only touches the parent dataset you named and its children;
- it only creates, lists and destroys snapshots named `@quince-*`;
- **it cannot destroy a dataset.** quince prints the `zfs destroy` command for you to run yourself;
- `rollback` accepts no flags, so it can never remove a newer snapshot — that is, a saved version;
- it refuses any request it does not recognise.

The *Test helper* button on the storage screen tells you whether all of this is working.
