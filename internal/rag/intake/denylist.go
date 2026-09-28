package intake

import (
	"path/filepath"
	"strings"
)

// secretDenylist lists the filename-shaped patterns that must never enter
// the index, regardless of Include (REQ-03, REQ-11, S-12). This is the
// single named location for the denylist — every backend's walk goes
// through Walk, so nothing else needs its own copy of this list. Patterns
// are matched against the file's base name only, via filepath.Match. Backup
// suffixes for PEM files are deliberately enumerated: filepath.Match cannot
// express "pem plus anything except documentation extensions", so a new
// suffix means adding a line here.
var secretDenylist = []string{
	".env",
	".env.*",
	"id_rsa*",
	"id_ed25519*",
	"id_ecdsa*",
	"id_dsa*",
	"*.pem",
	"*.p12",
	"*.pfx",
	"*.jks",
	"*.keystore",
	".npmrc",
	".netrc",
	".git-credentials",
	".pypirc",
	"auth.json",
	"kubeconfig",
	"*.kubeconfig",
	"kubeconfig.*",
	"*.key",
	"terraform.tfvars",
	"*.tfvars",
	"*.pem.bak",
	"*.pem.old",
	"*.pem.orig",
	"*.pem.save",
	"*.pem.backup",
}

// IsDenylisted reports whether base (a file's name, with no directory
// component) matches one of secretDenylist's patterns.
func IsDenylisted(base string) bool {
	base = strings.ToLower(base)
	for _, pattern := range secretDenylist {
		if ok, _ := filepath.Match(pattern, base); ok {
			return true
		}
	}
	return false
}

// isDenylisted preserves the package-private entry point used by older callers.
func isDenylisted(base string) bool { return IsDenylisted(base) }
