package storage

import "strings"

// AnchoredFilterRules returns the exact rclone exclude rules for quince's subtree under a
// whole-tree offsite sync (D5a). subdir is quince's directory under the transfer root (e.g.
// "iphone-backup" when syncing the storage parent). Every rule is ANCHORED (leading '/') so it
// matches only at the transfer root — an unanchored "**/working/**" would ALSO drop a
// same-named directory inside backup content under latest/, silently corrupting the offsite
// copy (the D5a hazard). The deploy docs ship this block verbatim; PathExcluded proves its
// semantics in CI, and lab gate 12 runs it through the real rclone.
//
// qn.5b: the per-job staging is now working/<udid> (the old per-job work/<job> is gone), so only
// working/ (the mutable in-progress tree) and versions/ (local-only namespace history) are excluded
// — latest/ is the sole synced payload, and the atomic exchange means a walk never sees it missing.
//
// THESE RULES ARE EXCLUSIONS, SO ON ZFS THEY NOW MATCH NOTHING (qn.6h). latest/ was never named here
// — it was synced by NOT being excluded — and after the in-place ruling that backend has no
// working/, no versions/ and no latest/ at all. The whole device dataset is therefore in scope, and
// DURING A BACKUP THAT IS A HALF-TRANSFERRED TREE, which a walk would upload as though it were a
// verified version. Nothing here can fix it: the fix is to read .zfs/snapshot/<snap>/ instead of the
// live tree, which is quince#735. Until that lands `deploy/storage.md` tells the operator to keep a
// zfs storage out of a whole-host rclone job — a cost accepted knowingly, not overlooked.
//
// qn.6c: the storage IDENTITY marker is excluded too, and the reason is not size or noise. Offsite
// is a REPLICATION of a storage, not a storage (the multi-storage epic's point 3). If
// quince-storage.json rides along, the replica carries its SOURCE's UUID and two places assert one
// identity — precisely the question the marker exists to answer. Excluding it keeps that fork open;
// including it would decide it silently.
//
// It needs its OWN rule because both existing rules match at `/<subdir>/*/…` and the marker sits at
// `/<subdir>/…`, one level shallower. Measured before it was written: without this line
// PathExcluded returns false for the marker, so it would have been synced (quince#378).
func AnchoredFilterRules(subdir string) []string {
	return []string{
		"- /" + subdir + "/*/working/**",
		"- /" + subdir + "/*/versions/**",
		"- /" + subdir + "/" + StorageMarkerName,
	}
}

// PathExcluded reports whether a transfer-root-relative path is excluded by any "- <pattern>"
// rule. Patterns support rclone's '*' (one path segment) and '**' (zero or more segments); a
// leading '/' anchors to the transfer root, otherwise the pattern may match at any depth.
func PathExcluded(rel string, rules []string) bool {
	relSegs := splitPath(rel)
	for _, rule := range rules {
		rule = strings.TrimSpace(rule)
		if !strings.HasPrefix(rule, "- ") {
			continue // only exclude rules matter for this walk
		}
		pat := strings.TrimSpace(strings.TrimPrefix(rule, "- "))
		if matchGlob(relSegs, splitPath(strings.TrimPrefix(pat, "/"))) {
			return true
		}
	}
	return false
}

func splitPath(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

// matchGlob matches path segments against pattern segments with '*' (one segment) and '**'
// (zero or more segments).
func matchGlob(rel, pat []string) bool {
	if len(pat) == 0 {
		return len(rel) == 0
	}
	if pat[0] == "**" {
		for i := 0; i <= len(rel); i++ {
			if matchGlob(rel[i:], pat[1:]) {
				return true
			}
		}
		return false
	}
	if len(rel) == 0 {
		return false
	}
	if pat[0] == "*" || pat[0] == rel[0] {
		return matchGlob(rel[1:], pat[1:])
	}
	return false
}
