import type { ConfigResponse, Device } from "@/lib/types";

// ONE PLACE DECIDES WHETHER ENCRYPTION BLOCKS A BACKUP, because three surfaces render off it: the
// banner's sentence, the disabled "Back up now", and the line under the action row saying why
// (quince#889). Two of those live in different components from the page that owns the facts, and a
// second copy of the condition is how a disabled button loses its reason.
//
// It is a pure function of the two facts the client already has — `backup.require_encryption` from
// GET /api/config and the device's own `backup_encryption` — so no wire change was needed for any
// of it (Operator ruling 2026-08-13: the guard is the UI's; POST /api/backups is unchanged).

// encryptionBlocksBackup is TRI-STATE, and `undefined` is not `false`.
//
// The policy arrives on a separate request from the device, so there is a window where quince
// cannot say. Answering `false` there would print "Health, Keychain and saved passwords are
// omitted" — the permissive policy's consequence — on an install that may be enforcing the strict
// one; answering `true` would disable a button on a policy nobody has read yet.
export function encryptionBlocksBackup(
  device: Device | undefined,
  config: ConfigResponse | undefined,
): boolean | undefined {
  if (device === undefined || config === undefined) return undefined;
  return config.config.backup.require_encryption && device.backup_encryption === "off";
}

// unencryptedConsequence is what follows "This device's backups are not encrypted" on the banner.
//
// Under `require_encryption: true` the old sentence — *Health, Keychain, and saved passwords are
// omitted* — was FALSE: nothing is omitted, because nothing is backed up. It was the most prominent
// text on the screen, describing the permissive policy while the daemon enforced the strict one.
export function unencryptedConsequence(blocks: boolean | undefined): string {
  if (blocks === undefined) return ".";
  return blocks
    ? " — and this quince only backs up encrypted devices, so nothing is being backed up."
    : " — Health, Keychain, and saved passwords are omitted.";
}
