package lane

import (
	"slices"
	"testing"

	"mrinspect/internal/gitlab"
)

func assertContainsTerms(t *testing.T, got []string, want ...string) {
	t.Helper()
	for _, term := range want {
		if !slices.Contains(got, term) {
			t.Errorf("Terms() = %v, want term %q", got, term)
		}
	}
}

// TestTerms_ExtractedFromDiff verifies REQ-02 / S-36: retrieval terms come
// from changed paths and identifiers in both sides of a unified diff. Sharing
// one Terms slice across lanes is compose-level behavior covered by T05.
func TestTerms_ExtractedFromDiff(t *testing.T) {
	t.Run("path and camelCase identifier", func(t *testing.T) {
		changes := []gitlab.Change{{
			OldPath: "internal/service/batch.go",
			NewPath: "internal/service/batch.go",
			Diff: `diff --git a/internal/service/batch.go b/internal/service/batch.go
index 1111111..2222222 100644
--- a/internal/service/batch.go
+++ b/internal/service/batch.go
@@ -1 +1 @@
-func previousOperation() {}
+func beginTransaction() {}
`,
		}}

		got := Terms(changes)
		if len(got) == 0 {
			t.Error("Terms() returned no terms for a non-empty MR diff")
		}
		assertContainsTerms(t, got, "batch", "service", "go", "begin", "transaction")
	})

	t.Run("snake_case identifier", func(t *testing.T) {
		changes := []gitlab.Change{{
			OldPath: "internal/worker/retry.go",
			NewPath: "internal/worker/retry.go",
			Diff: `diff --git a/internal/worker/retry.go b/internal/worker/retry.go
index 1111111..2222222 100644
--- a/internal/worker/retry.go
+++ b/internal/worker/retry.go
@@ -1 +1 @@
-var attempts = 0
+var retry_count = 3
`,
		}}

		assertContainsTerms(t, Terms(changes), "retry", "count")
	})

	t.Run("removed line identifier", func(t *testing.T) {
		changes := []gitlab.Change{{
			OldPath: "internal/store/session.go",
			NewPath: "internal/store/session.go",
			Diff: `diff --git a/internal/store/session.go b/internal/store/session.go
index 1111111..2222222 100644
--- a/internal/store/session.go
+++ b/internal/store/session.go
@@ -1 +1 @@
-func revokeSessionToken() {}
+func replacement() {}
`,
		}}

		assertContainsTerms(t, Terms(changes), "revoke", "session", "token")
	})

	t.Run("first forty cap", func(t *testing.T) {
		changes := []gitlab.Change{{
			OldPath: "internal/limits/cap.go",
			NewPath: "internal/limits/cap.go",
			Diff: `diff --git a/internal/limits/cap.go b/internal/limits/cap.go
index 1111111..2222222 100644
--- a/internal/limits/cap.go
+++ b/internal/limits/cap.go
@@ -0,0 +1,45 @@
+var itemalpha = 1
+var itembravo = 2
+var itemcharlie = 3
+var itemdelta = 4
+var itemecho = 5
+var itemfoxtrot = 6
+var itemgolf = 7
+var itemhotel = 8
+var itemindia = 9
+var itemjuliet = 10
+var itemkilo = 11
+var itemlima = 12
+var itemmike = 13
+var itemnovember = 14
+var itemoscar = 15
+var itempapa = 16
+var itemquebec = 17
+var itemromeo = 18
+var itemsierra = 19
+var itemtango = 20
+var itemuniform = 21
+var itemvictor = 22
+var itemwhiskey = 23
+var itemxray = 24
+var itemyankee = 25
+var itemzulu = 26
+var itemamber = 27
+var itembirch = 28
+var itemcedar = 29
+var itemdahlia = 30
+var itemelmwood = 31
+var itemfirwood = 32
+var itemgranite = 33
+var itemhazel = 34
+var itemivory = 35
+var itemjuniper = 36
+var itemkrypton = 37
+var itemlilac = 38
+var itemmaple = 39
+var itemnickel = 40
+var itemonyx = 41
+var itemquartz = 42
+var itemruby = 43
+var itemsilver = 44
+var itemtopaz = 45
`,
		}}

		got := Terms(changes)
		if len(got) != 40 {
			t.Errorf("len(Terms()) = %d, want exactly 40: %v", len(got), got)
		}
		if !slices.Contains(got, "itemalpha") {
			t.Errorf("Terms() = %v, want early extraction-order term %q", got, "itemalpha")
		}
		if slices.Contains(got, "itemtopaz") {
			t.Errorf("Terms() = %v, want term beyond the first 40 %q to be absent", got, "itemtopaz")
		}
	})

	t.Run("stopwords and diff noise removed", func(t *testing.T) {
		changes := []gitlab.Change{{
			OldPath: "internal/noise/check.go",
			NewPath: "internal/noise/check.go",
			Diff: `diff --git a/internal/noise/check.go b/internal/noise/check.go
index 1111111..2222222 100644
--- a/internal/noise/check.go
+++ b/internal/noise/check.go
@@ -1 +1 @@
-if the oldWidget != nil {}
+if the usefulWidget != nil {}
`,
		}}

		got := Terms(changes)
		assertContainsTerms(t, got, "useful", "widget")
		for _, unwanted := range []string{"the", "if", "diff", "git", "index"} {
			if slices.Contains(got, unwanted) {
				t.Errorf("Terms() = %v, want stopword or diff noise %q to be absent", got, unwanted)
			}
		}
	})
}
