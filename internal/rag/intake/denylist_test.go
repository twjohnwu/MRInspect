package intake

import "testing"

// TestDenylist_OriginalSecretNames verifies REQ-03 / REQ-11: the nine
// literal secret-shaped names required by S-12 remain refused while T24
// widens coverage for their common real-world variants.
func TestDenylist_OriginalSecretNames(t *testing.T) {
	t.Parallel()

	for _, name := range []string{
		".env",
		"id_rsa",
		"tls.pem",
		".npmrc",
		".netrc",
		".git-credentials",
		"kubeconfig",
		"signing.key",
		"terraform.tfvars",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if !isDenylisted(name) {
				t.Errorf("isDenylisted(%q) = false, want true", name)
			}
		})
	}
}

// TestDenylist_WidenedSecretFilenames verifies REQ-03 / REQ-11 / T24: the
// filename denylist is the sole secret-protection gate, so it must catch the
// common prefix, suffix, and backup forms that the former exact matches miss.
func TestDenylist_WidenedSecretFilenames(t *testing.T) {
	t.Parallel()

	for _, name := range []string{
		"admin.kubeconfig",
		"kubeconfig.yaml",
		"prod.tfvars",
		"secret.auto.tfvars",
		"server.pem.bak",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if !isDenylisted(name) {
				t.Errorf("isDenylisted(%q) = false, want true", name)
			}
		})
	}
}

// TestDenylist_LegitimateDocsPass verifies REQ-03 / REQ-11 / T24: widening
// the secret-shaped filename patterns must not turn ordinary documentation
// into a denylist match.
func TestDenylist_LegitimateDocsPass(t *testing.T) {
	t.Parallel()

	for _, name := range []string{
		"kubeconfig-guide.md",
		"terraform.md",
		"README.pem.md",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if isDenylisted(name) {
				t.Errorf("isDenylisted(%q) = true, want false", name)
			}
		})
	}
}
