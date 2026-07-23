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
func AnchoredFilterRules(subdir string) []string {
	return []string{
		"- /" + subdir + "/*/working/**",
		"- /" + subdir + "/*/versions/**",
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
