# Spike: libimobiledevice from-source patched build (qn.6b)

**Goal of spike:** decision-grade facts for switching the Alpine image from
`apk add libimobiledevice-progs` to building libimobiledevice **from a pinned upstream
tag with in-tree patch files** to (a) raise the receive timeout per issue #1413 and
(b) add a `--gate <path>` pause point in `idevicebackup2`.

All findings verified against actual upstream source. Repos cloned and read at:

- `libimobiledevice/libimobiledevice` — **master HEAD `fa0f791`** (`fa0f79190142bc309307967c058f89c1b36eb6b8`, committed 2026-06-10). Newest release tag **`1.4.0`** (2025-10-10).
- Alpine package facts from `pkgs.alpinelinux.org` branch **v3.24**, x86_64/community.

Line numbers below are from master HEAD `fa0f791`. Where the newest release tag `1.4.0`
differs materially it is called out (for the files that matter here, `1.4.0` and master
are identical — see notes).

---

## Decision-grade summary (read this first)

1. **Pin tag `1.4.0`** (commit `149f7623`, 2025-10-10) — the newest and only recent
   release. Master HEAD (`fa0f791`, 2026-06-10) is only ~cleanup commits ahead; the
   three files we touch (`property_list_service.c`, `service.c`, `tools/idevicebackup2.c`)
   are byte-identical between `1.4.0` and master for our purposes. Prefer the tag for
   reproducibility.
2. **Build ONE repo, not four.** Alpine 3.24 community ships every `-dev` dependency at a
   version that satisfies `configure.ac`'s minimums: `libplist-dev 2.7.0` (needs ≥2.3.0),
   `libimobiledevice-glue-dev 1.3.2` (needs ≥1.3.0), `libusbmuxd-dev 2.1.1` (needs ≥2.0.2),
   and **`libtatsu-dev 1.0.5`** (needs ≥1.0.3). Only `libimobiledevice` itself is built
   from source + patched.
3. **RISK — undocumented 4th dep:** current libimobiledevice requires **`libtatsu`**
   (`PKG_CHECK_MODULES(libtatsu, libtatsu-1.0 >= 1.0.3)`), which is NOT in the task's
   assumed dep list. It IS packaged in Alpine 3.24 (`libtatsu-dev 1.0.5-r0`), so no extra
   build — but the Dockerfile must `apk add libtatsu-dev` or configure fails.
4. **The #1413 patch is TWO one-line constant changes in SHARED LIBRARY files, not
   `idevicebackup2.c`:** `src/property_list_service.c:272` (`30000` → `900000`) and
   `src/service.c:179` (`30000` → `900000`). 15 min = `900000` ms. This is exactly what
   the #1413 thread converged on (users raised both to 90000+, and note "up to 15 minutes"
   for large devices).
5. **#1413 = SSL error `-4`, not timeout `-5` — nuance that is discussed in-thread and
   matters.** `-4` = `MOBILEBACKUP2_E_SSL_ERROR` (fatal); `-5` = `MOBILEBACKUP2_E_RECEIVE_TIMEOUT`
   (the loop *retries* this, non-fatal). A **clean** socket timeout during an SSL read
   returns `-5` (retried forever). `-4` fires when the SSL read returns short *without* the
   timeout being flagged (peer reset / SSL-layer error). Raising the two `30000` constants
   is the community-accepted fix and multiple users confirm it works — but understand it
   works by giving the slow passcode/large-device transfer more time before the SSL layer
   trips, **not** by changing a clean-timeout code path. Treat "raising the timeout fully
   fixes #1413" as EMPIRICALLY-BACKED-BUT-NOT-MECHANICALLY-PROVEN.
6. **OpenSSL is the default crypto backend** (`default_openssl=yes`, `configure.ac`).
   `apk add openssl-dev`. (The #1413 thread's GnuTLS-vs-OpenSSL tangent is moot; Alpine
   already uses OpenSSL.)
7. **Gate insertion point is exactly ONE clean line, and it exists as assumed.** Insert the
   poll-until-gate-file block in `tools/idevicebackup2.c` **between line 2279 and line 2280**
   — i.e. at the end of the `if (err == MOBILEBACKUP2_E_SUCCESS)` success branch of
   `case CMD_BACKUP`, after the passcode-observe block, before the closing `} else {`.
   This is after `mobilebackup2_send_request(..., "Backup", ...)` (line 2261) and before the
   `do { ... mobilebackup2_receive_message ... } while (1)` loop (starts line 2504, first
   receive line 2507).
8. **Info.plist is remove-then-created BEFORE the Backup request** (`remove_file(info_path)`
   line 2242, `plist_write_to_file(...)` line 2243, request sent line 2261) — as assumed.
   No `Status.plist` / `Manifest.db` read in the backup path pre-request (those arrive later
   via `DLMessageDownloadFiles`). CONFIRMED.
9. **No long-lived fd into the target dir.** The loop does per-file `fopen`/`fclose`
   (`mb2_handle_receive_files`: `remove_file`+`fopen` line 1308–1309, `fclose` line 1346;
   `mb2_handle_send_file`: `fopen` 1044 / `fclose` 1084). No persistent `DIR*`/fd is held
   across messages → quince can safely mutate/seed the target dir while paused at the gate.
   CONFIRMED.
10. **Passcode nuance for the gate:** the in-code "passcode wait" (lines 2270–2279) is only a
    best-effort 2 s *observation* window (it watches the `com.apple.LocalAuthentication.ui.presented`
    notification and prints a message); it does NOT block until unlock. The real
    block-until-unlocked happens at the loop's first `mobilebackup2_receive_message`
    (line 2507). So the gate at line 2279 sits *after the passcode UI has been presented/
    observed* and *before* the first receive — correct — but be aware the device may already
    be waiting; holding the gate a long time relies on TCP buffering the device's first
    message. Design consideration for quince, not a blocker.
11. **STALE ASSUMPTION in the task brief:** Alpine 3.24 does NOT package
    `1.1.1_git20250201`; it ships **`libimobiledevice 1.4.0-r0`** (and `-progs`, `-dev` at
    1.4.0-r0). The `1.1.1_git<date>` scheme is the *older* Alpine naming from before the
    1.4.0 release (a pre-1.4.0 master snapshot ~2025-02-01); 3.24 moved to the real 1.4.0.
    This strengthens the plan: pin `1.4.0` to match what Alpine's family packages were
    built against.
12. **Build system:** a `git` checkout of a tag has **no `configure`** — you must run
    `./autogen.sh` (needs `autoconf automake libtool pkgconf`). The **release dist tarball**
    `libimobiledevice-1.4.0.tar.bz2` (attached to the GH release) ships a pre-generated
    `configure`. Since we apply patches anyway, `git clone --branch 1.4.0` + patch +
    `./autogen.sh` is the clean path; alternatively fetch+patch the dist tarball and skip
    autotools. `CFLAGS` unconditionally appends `-Werror` (`configure.ac:88`) — watch for
    build breaks on Alpine's GCC; can be neutralised with `CFLAGS=-Wno-error` at
    `./configure` time if needed.

---

## A. The #1413 receive-timeout fix

### A1. Issue #1413 — symptom, discussion, resolution
**URL:** https://github.com/libimobiledevice/libimobiledevice/issues/1413
**Title:** *"ERROR: Could not receive from mobilebackup2 (-4)"*

**Symptom:** `idevicebackup2` negotiates the protocol, requests a full backup from an
iOS 16.1.1 device, then aborts with `ERROR: Could not receive from mobilebackup2 (-4)`
after receiving zero files. Reporters tie it to newer iOS and to Wi-Fi/network transport.

**Discussion / resolution (from the issue comments):**
- `outsourcestudio` (2023-02): device logs showed the device took **62 s** to start the
  backup; raised the default receive timeout **30 s → 90 s** and it worked.
- `pogopaule` (2023-02): asked to confirm the location — **`property_list_service.c` line 272**
  — and explicitly noted **"-4" is an SSL error, not the timeout error "-5"**.
- `outsourcestudio` (2023-02): fix = replace **`30000` with `90000` in TWO places**:
  **`service.c` in `service_receive()`** and **`property_list_service.c` in
  `property_list_service_receive_plist()`**. Backup then succeeded.
- `mexmer` (2024-11) / large-device reports: timeouts may need to reach **~15 minutes** for
  1 TB phones / 100k+ photos; suggested per-tool optional timeout CLI params.
- `arksunix`/`OctopusET`: GnuTLS→OpenSSL alone did NOT fix it; the timeout raise to 90000
  was still required.
- 2026 comments (`maduranma`, `lbr77`): random Wi-Fi/netmuxd failures persist on large
  backups; packet capture showed **device-initiated connection resets** — i.e. some `-4`
  cases are the *device* dropping the TLS connection, which a longer timeout cannot fix.

**Takeaway:** the thread's accepted fix is exactly our plan (raise both `30000` constants);
the target 15 min matches the large-device guidance. But `-4` is genuinely an SSL error
code, and some `-4` occurrences are device resets that a timeout bump won't cure. See A2/RISK.

### A2. The receive-timeout constant in current source
The mobilebackup2 receive path:

```
mobilebackup2_receive_message()                       src/mobilebackup2.c:217
  → device_link_service_receive_message()             src/device_link_service.c:360
      → property_list_service_receive_plist()          src/property_list_service.c:270
          → internal_plist_receive_timeout(client, plist, 30000)   ← the 30 s default
```

**Primary constant — `src/property_list_service.c:270-273`** (governs the blocking read of
the *first* 4-byte packet-length word, i.e. the wait for the device's next message):
```c
property_list_service_error_t property_list_service_receive_plist(property_list_service_client_t client, plist_t *plist)
{
	return internal_plist_receive_timeout(client, plist, 30000);
}
```
`git blame` line 272 → last touched commit `72456f2` (Nikias Bassen, 2025-06-07), value
still `30000`. Inside `internal_plist_receive_timeout`, the initial length read uses this
value (`service_receive_with_timeout(..., timeout)` line 194) and returns
`PROPERTY_LIST_SERVICE_E_RECEIVE_TIMEOUT` on a 0-byte read (lines 200-203).

**Secondary constant — `src/service.c:177-180`** (governs the *body/payload* reads that
follow the length word — `internal_plist_receive_timeout` reads the payload via bare
`service_receive()` at `property_list_service.c:219`):
```c
service_error_t service_receive(service_client_t client, char* data, uint32_t size, uint32_t *received)
{
	return service_receive_with_timeout(client, data, size, received, 30000);
}
```

So the **effective 30 s default is a hardcoded `30000` ms argument at a call site**, in
**two** shared-library files — NOT a `#define`, and NOT in `idevicebackup2.c`.

**How a timeout becomes an error code (the `-4` vs `-5` mechanism), verified in `src/idevice.c`:**
- SSL read loop: `idevice_connection_receive_timeout()` `src/idevice.c:778`. On a short read
  it returns (line 835-837):
  ```c
  if (received < len) {
      *recv_bytes = received;
      return connection->status == IDEVICE_E_SUCCESS ? IDEVICE_E_SSL_ERROR : connection->status;
  }
  ```
- The BIO callback `internal_ssl_read()` `src/idevice.c:972` sets `connection->status = res`
  (line 993) — `IDEVICE_E_TIMEOUT` on a clean socket timeout.
- Therefore: **clean socket timeout → `IDEVICE_E_TIMEOUT` → `…_RECEIVE_TIMEOUT` → mobilebackup2
  `-5`** (retried, see C10). **Short read with status still SUCCESS (peer reset / SSL-layer
  fault) → `IDEVICE_E_SSL_ERROR` → mobilebackup2 `-4`** (fatal).
- Error enum (`include/libimobiledevice/mobilebackup2.h:39-48`): `-3` MUX, **`-4` SSL_ERROR**,
  **`-5` RECEIVE_TIMEOUT**, `-6` BAD_VERSION, `-7` REPLY_NOT_OK.

### A3. What a minimal 15-minute patch changes
15 min = **`900000` ms**. Two one-line edits, both in **shared library files** (patch files
applied to the libimobiledevice source tree, NOT `idevicebackup2.c`):

| File | Line | Change |
|------|------|--------|
| `src/property_list_service.c` | 272 | `internal_plist_receive_timeout(client, plist, 30000)` → `... 900000` |
| `src/service.c` | 179 | `service_receive_with_timeout(client, data, size, received, 30000)` → `... 900000` |

- It is a **changed argument at a call site**, in each of two shared files — not a `#define`.
- **Side effect (accept or narrow):** these are the *default* receive timeouts for ALL
  services (lockdown handshakes, etc.), so every default receive now waits up to 15 min.
  For quince's single-purpose backup image this is acceptable. A narrower alternative
  (change only the mobilebackup2 path) would still have to touch a shared file, because the
  payload reads at `property_list_service.c:219` go through `service.c`'s `service_receive()`
  regardless — so the two-constant patch is the simplest *complete* change and matches the
  #1413 consensus. `idevicebackup2.c` is not touched by the timeout fix.

---

## B. Pinned tag + build-from-source facts

### B4. Latest release tag / master HEAD / Alpine correspondence
- **Newest release tag: `1.4.0`** — commit `149f7623c672c1fa73122c7119a12bfc0012f2ac`,
  2025-10-10. Verified via `git for-each-ref` (creatordate order: … `1.2.0` 2015, `1.3.0`
  2020, `1.4.0` 2025-10-10 — a 5-year release gap, as the project is famous for) and via the
  GH "latest release" API (`tag_name: 1.4.0`, asset `libimobiledevice-1.4.0.tar.bz2`,
  714,628 bytes).
- **Master HEAD: `fa0f791`** (`fa0f79190142bc309307967c058f89c1b36eb6b8`, 2026-06-10,
  *"tools/afcclient: Start in /Documents when using --documents"*). Only minor commits ahead
  of `1.4.0`; none touch our three files.
- **Alpine 3.24 ships `libimobiledevice 1.4.0-r0`** (and `-dev`, `-progs`, `-doc` at
  1.4.0-r0), community repo. **The task brief's `1.1.1_git20250201` is stale** — that is the
  older Alpine naming (arbitrary `1.1.1` base + a master-snapshot date), used before 1.4.0
  existed; `20250201` ≈ a master snapshot from ~2025-02-01 (pre-1.4.0). Exact commit for that
  snapshot: **UNVERIFIED** (not needed — 3.24 is 1.4.0). Recommendation: pin `1.4.0`.

### B5. Build dependencies on Alpine 3.24 (musl) — availability
`configure.ac` `PKG_CHECK_MODULES` (lines 44-47) requires four libs; all present in Alpine
3.24 community as `-dev` packages at satisfying versions:

| Dependency (pkg-config module) | configure.ac min | Alpine 3.24 `-dev` pkg | OK? |
|---|---|---|---|
| `libplist-2.0` | ≥ 2.3.0 | `libplist-dev` **2.7.0-r1** | ✅ |
| `libimobiledevice-glue-1.0` | ≥ 1.3.0 | `libimobiledevice-glue-dev` **1.3.2-r0** | ✅ |
| `libusbmuxd-2.0` | ≥ 2.0.2 | `libusbmuxd-dev` **2.1.1-r0** | ✅ |
| `libtatsu-1.0` | ≥ 1.0.3 | `libtatsu-dev` **1.0.5-r0** | ✅ |
| `openssl` | ≥ 0.9.8 | `openssl-dev` (Alpine openssl 3.x) | ✅ |

Plus toolchain: `build-base` (gcc/musl-dev/make), `autoconf automake libtool pkgconf`,
`git`. **⇒ Build ONLY `libimobiledevice` from source; do NOT build a chain of four.**
The task's dep list omitted **`libtatsu`** — it is required and must be `apk add`ed
(`libtatsu-dev`), but needs no from-source build.

*(Package versions read from pkgs.alpinelinux.org branch v3.24; all maintained in
community/x86_64.)*

### B6. Release tarball vs `./autogen.sh`
- `configure` is **not** checked into git (`ls configure` → not found). A **git checkout of a
  tag therefore REQUIRES `./autogen.sh`** — which runs `libtoolize`/`glibtoolize`, `aclocal
  -I m4`, `autoheader`, `automake --add-missing`, `autoconf`, then `configure` (verified:
  `autogen.sh` contents). Needs `autoconf automake libtool pkgconf` (+ `m4`).
- The **release dist tarball `libimobiledevice-1.4.0.tar.bz2`** (a `make dist` artifact
  attached to the GH release, distinct from GitHub's auto-generated "Source code" archives)
  ships a pre-generated `configure`, so `./configure && make` works without autotools.
- **Recommendation:** since we apply patch files, `git clone --branch 1.4.0 --depth 1` +
  apply patches + `./autogen.sh && make && make install` is the clean, reproducible path.
  (Or: download the dist tarball, `patch -p1`, `./configure` — avoids autotools but you must
  host/verify the tarball.)

### B7. Version coupling
`configure.ac` (master and tag `1.4.0` identical here) hardcodes the minimums shown in B5:
`LIBUSBMUXD_VERSION=2.0.2`, `LIBPLIST_VERSION=2.3.0`, `LIMD_GLUE_VERSION=1.3.0`,
`LIBTATSU_VERSION=1.0.3` (`configure.ac:28-31`, checked at lines 44-47). **Every Alpine 3.24
`-dev` package exceeds these**, so we CAN lean on Alpine's `-dev` packages and do not need to
build the family at matching tags. (Makes sense: Alpine built its own `libimobiledevice
1.4.0-r0` against these same 3.24 libs.) No newer-than-Alpine minimum exists on master.

**Build-hardening note:** `configure.ac:88` does `CFLAGS+=" $libplist_CFLAGS -Werror"`
unconditionally. On a newer Alpine GCC this can fail the build on benign warnings; if so,
pass `CFLAGS="-Wno-error"` (or `--disable-...`/sed the line) in the Dockerfile.

---

## C. The gate point in `idevicebackup2` (`tools/idevicebackup2.c`, HEAD `fa0f791`)

`case CMD_BACKUP:` begins at **line 2209**. (Note: `1.4.0` and master are identical in this
region.)

### C8. Where the `Backup` request is sent
**`tools/idevicebackup2.c:2261`:**
```c
err = mobilebackup2_send_request(mobilebackup2, "Backup", udid, source_udid, opts);
```

### C9. The iOS 16.1+ passcode wait, relative to the request
**`tools/idevicebackup2.c:2270-2279`**, immediately after the request, inside the
`if (err == MOBILEBACKUP2_E_SUCCESS)` branch:
```c
if (device_version >= IDEVICE_DEVICE_VERSION(16,1,0)) {
    /* let's wait 2 second to see if the device passcode is requested */
    int retries = 20;
    while (retries-- > 0 && !passcode_requested) {
        usleep(100000);
    }
    if (passcode_requested) {
        printf("*** Waiting for passcode to be entered on the device ***\n");
    }
}
```
`passcode_requested` is set by the notification callback `notify_cb`
(**lines 110-127**) on the device notification `com.apple.LocalAuthentication.ui.presented`
(**line 120-121**) and cleared on `…ui.dismissed` (122-123).

**Confirmation:** the passcode prompt is presented by the *device* in response to the `Backup`
request, and idevicebackup2 *observes* it here (BEFORE the main message loop). **Nuance:**
this block does NOT block until unlock — it is a fixed 2 s (20×100 ms) observation window that
just prints a message. The genuine "wait for the user to unlock" manifests one step later, at
the loop's first `mobilebackup2_receive_message` (line 2507), which blocks up to the receive
timeout for the device's first `DLMessage*`.

### C10. The main message loop
**Loop head `tools/idevicebackup2.c:2504`** (inside `if (cmd != CMD_LEAVE) {` at line 2491),
first receive at **2507**:
```c
do {                                                              // line 2504
    free(dlmsg); dlmsg = NULL;
    mberr = mobilebackup2_receive_message(mobilebackup2, &message, &dlmsg);   // line 2507
    if (mberr == MOBILEBACKUP2_E_RECEIVE_TIMEOUT) {               // -5: RETRY, not fatal
        PRINT_VERBOSE(2, "Device is not ready yet, retrying...\n");
        goto files_out;                                          // line 2510
    } else if (mberr != MOBILEBACKUP2_E_SUCCESS) {               // e.g. -4 SSL: fatal
        PRINT_VERBOSE(0, "ERROR: Could not receive from mobilebackup2 (%d)\n", mberr);  // 2512  ← #1413's message
        quit_flag++;
        goto files_out;
    }
    ...dispatch...
} while (1);                                                      // line 2765
```
Dispatch handlers (`strcmp(dlmsg, …)`): `DLMessageDownloadFiles` (2517),
`DLMessageUploadFiles` (2523), `DLMessageGetFreeDiskSpace` (2527), `DLMessagePurgeDiskSpace`
(2546), `DLMessageCreateDirectory` (2554), `DLMessageMoveFiles`/`DLMessageMoveItems` (2557),
`DLMessageRemoveFiles`/`DLMessageRemoveItems` (2610), `DLMessageCopyItem` (2656),
`DLMessageDisconnect` (2691), `DLMessageProcessMessage` (2693).

`files_out:` (line 2748) frees the message, and only `break`s the loop if `quit_flag > 0`
(lines 2754-2764). So **`-5` RECEIVE_TIMEOUT re-enters `while(1)` and retries indefinitely**;
**`-4` SSL_ERROR sets `quit_flag++` → break → fatal** (this is #1413's exact failure and its
printed message at line 2512).

### C11. The single clean gate insertion point — CONFIRMED (exactly one)
There is exactly one point satisfying "after the `Backup` request is sent (passcode has been
presented/observed) and before the first `mobilebackup2_receive_message` of the loop":

**Insert between line 2279 and line 2280** — the end of the
`if (err == MOBILEBACKUP2_E_SUCCESS)` success branch of `case CMD_BACKUP`, i.e. right after
the passcode-observe `}` (2279) and before the `} else {` (2280):

```c
        if (passcode_requested) {
            printf("*** Waiting for passcode to be entered on the device ***\n");
        }
    }                                   // <-- line 2279 (end of the device_version>=16.1 block)

    /* --gate: poll until the gate file appears, then proceed */     // <-- INSERT HERE
    if (gate_path) {
        while (access(gate_path, F_OK) != 0 && !quit_flag) {
            usleep(100000);             // 100 ms poll
        }
    }

} else {                                // <-- line 2280 (err != SUCCESS)
```

Why this is the only clean point:
- It is **after** the `"Backup"` request (2261) → the passcode UI has been presented and
  observed (2270-2279).
- It is **inside the CMD_BACKUP success branch** → it gates *only* successful backups, not
  restore/info/list and not the error path.
- It is **before** `break` (2290) and therefore before the `do{…}while(1)` loop (2504) and its
  first `mobilebackup2_receive_message` (2507) → inserting a blocking wait here cannot
  desync the protocol: no receive has happened yet, and the device's first `DLMessage*` will
  simply sit in the TCP/SSL buffer until the gate releases and the loop's first receive reads
  it. (A generic alternative — just before `do {` at line 2504 guarded by `cmd == CMD_BACKUP`
  — also works but is less localized; the in-case point above is cleaner.)

Requires a small amount of plumbing (a `--gate <path>` long-opt into the getopt table around
line 1762 and a `static const char *gate_path` global) — mechanical, no protocol impact.

### C12. Info.plist handling pre-request — CONFIRMED as assumed
`tools/idevicebackup2.c:2232-2261`, in order:
```c
info_plist = mobilebackup_factory_info_plist_new(udid, device, afc);   // 2237  build dict
...
remove_file(info_path);                                                 // 2242  remove old
plist_write_to_file(info_plist, info_path, PLIST_FORMAT_XML, 0);        // 2243  write new
...
err = mobilebackup2_send_request(mobilebackup2, "Backup", udid, source_udid, opts);  // 2261
```
So it **remove-then-creates `Info.plist` BEFORE sending `Backup`** — as assumed. The Info dict
is generated in-memory from the device via AFC/lockdown (`mobilebackup_factory_info_plist_new`,
line 345); it does not read the backup target's `Info.plist` to build it in the backup path.
**No `Status.plist` or `Manifest.db` read pre-request** in the backup path — `Status.plist` is
only consulted in `case CMD_RESTORE` (`mb2_status_check_snapshot_state`, line 2295);
`Manifest.db` and the rest arrive later via `DLMessageDownloadFiles`/`UploadFiles`. CONFIRMED.
(`Info.plist` is read pre-command only for *restore/verify* paths, e.g. lines 1993-1999 and
2146-2157, not for backup.)

### C13. Long-lived fds into the target dir — CONFIRMED none
Per-file open/close, no persistent handle:
- **Receive (device→computer), `mb2_handle_receive_files` / its inner writer:**
  `remove_file(bname)` then `f = fopen(bname, "wb")` (**lines 1308-1309**), write chunks,
  `fclose(f)` (**line 1346**) — one open/close **per file, per message**.
- **Send (computer→device), `mb2_handle_send_file`:** `fopen(localfile, "rb")` (**line 1044**),
  `fclose(f)` (**lines 1084 / 1121**) — per file.
- The only long-lived handle is `lockfile` on the *device* via AFC
  (`afc_file_open("/com.apple.itunes.lock_sync", …)` line 2166 / `afc_file_close` line 2861) —
  that is on the phone, not the local target dir.

⇒ No local fd or `DIR*` is held open across the message loop into the backup target
directory. **quince can seed/mutate the target dir while idevicebackup2 is paused at the gate**
(which is before the loop even starts), with no fd-staleness hazard. CONFIRMED.

---

## Sources
- Issue #1413: https://github.com/libimobiledevice/libimobiledevice/issues/1413
- Source (master `fa0f791`, tag `1.4.0` `149f7623`): https://github.com/libimobiledevice/libimobiledevice
  — files cited: `src/property_list_service.c`, `src/service.c`, `src/idevice.c`,
  `src/device_link_service.c`, `src/mobilebackup2.c`, `include/libimobiledevice/mobilebackup2.h`,
  `tools/idevicebackup2.c`, `configure.ac`, `autogen.sh`.
- Alpine 3.24 packages (community/x86_64): https://pkgs.alpinelinux.org (branch v3.24) —
  libimobiledevice 1.4.0-r0, libplist-dev 2.7.0-r1, libimobiledevice-glue-dev 1.3.2-r0,
  libusbmuxd-dev 2.1.1-r0, libtatsu-dev 1.0.5-r0.
