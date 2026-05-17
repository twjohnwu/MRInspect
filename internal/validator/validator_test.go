package validator

import (
	"fmt"
	"strings"
	"testing"

	"mrinspect/internal/config"
	"mrinspect/internal/gitlab"
)

func testValidator() *Validator {
	return New(config.Config{
		Validation: config.ValidationConfig{
			MaxDiffSizeKB:     300,
			MaxFilesChanged:   50,
			AllowedExtensions: []string{".go", ".ts", ".py", ".yaml", ".md"},
		},
	})
}

func assertValidationCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected ValidationError with code %q, got nil", code)
	}
	ve, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("expected *ValidationError, got %T: %v", err, err)
	}
	if ve.Code != code {
		t.Errorf("code: want %q, got %q (message: %s)", code, ve.Code, ve.Message)
	}
}

func TestValidateDiff(t *testing.T) {
	v := testValidator()

	t.Run("empty diff", func(t *testing.T) {
		_, err := v.ValidateDiff("")
		assertValidationCode(t, err, "EMPTY_DIFF")
	})

	t.Run("diff too large", func(t *testing.T) {
		big := strings.Repeat("x", 301*1024)
		_, err := v.ValidateDiff(big)
		assertValidationCode(t, err, "DIFF_TOO_LARGE")
	})

	t.Run("valid diff with supported extension", func(t *testing.T) {
		diff := "--- a/main.go\n+++ b/main.go\n@@ -1 +1 @@\n+fix\n"
		res, err := v.ValidateDiff(diff)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.FilesChanged != 1 {
			t.Errorf("FilesChanged: want 1, got %d", res.FilesChanged)
		}
		if res.SupportedFiles != 1 {
			t.Errorf("SupportedFiles: want 1, got %d", res.SupportedFiles)
		}
	})

	t.Run("unsupported extension only", func(t *testing.T) {
		diff := "--- a/binary.exe\n+++ b/binary.exe\n@@ -1 +1 @@\n+data\n"
		_, err := v.ValidateDiff(diff)
		assertValidationCode(t, err, "NO_SUPPORTED_FILES")
	})

	t.Run("too many files", func(t *testing.T) {
		var sb strings.Builder
		for i := 0; i < 51; i++ {
			sb.WriteString(fmt.Sprintf("--- a/file%d.go\n+++ b/file%d.go\n@@ -1 +1 @@\n+fix\n", i, i))
		}
		_, err := v.ValidateDiff(sb.String())
		assertValidationCode(t, err, "TOO_MANY_FILES")
	})
}

func TestValidateMergeRequest(t *testing.T) {
	v := testValidator()

	tests := []struct {
		name    string
		mr      gitlab.MergeRequest
		wantErr bool
		code    string
	}{
		{"zero IID", gitlab.MergeRequest{IID: 0, Title: "title"}, true, "INVALID_MR"},
		{"empty title", gitlab.MergeRequest{IID: 1, Title: ""}, true, "INVALID_MR"},
		{"valid MR", gitlab.MergeRequest{IID: 1, Title: "feat: add thing"}, false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := v.ValidateMergeRequest(tt.mr)
			if tt.wantErr {
				assertValidationCode(t, err, tt.code)
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestValidateReviewContent(t *testing.T) {
	v := testValidator()

	// base is > 100 chars and contains all required sections
	base := strings.Repeat("x", 100) + "\n## Code Review\n### Findings\n### Verdict\n"

	tests := []struct {
		name    string
		content string
		code    string
	}{
		{"empty", "", "REVIEW_TOO_SHORT"},
		{"too short", "## Findings Verdict", "REVIEW_TOO_SHORT"},
		{"missing ##", strings.ReplaceAll(base, "##", "XX"), "MISSING_SECTION"},
		{"missing Findings", strings.ReplaceAll(base, "Findings", ""), "MISSING_SECTION"},
		{"missing Verdict", strings.ReplaceAll(base, "Verdict", ""), "MISSING_SECTION"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertValidationCode(t, v.ValidateReviewContent(tt.content), tt.code)
		})
	}

	t.Run("valid content", func(t *testing.T) {
		if err := v.ValidateReviewContent(base); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestSanitizeInput(t *testing.T) {
	v := testValidator()

	tests := []struct {
		name     string
		input    string
		contains string
		absent   string
	}{
		{"script tag escaped", "<script>alert(1)</script>", "&lt;script&gt;", "<script>"},
		{"javascript: removed", "click javascript:void(0)", "", "javascript:"},
		{"onclick removed", `<a onclick="evil()">link</a>`, "", "onclick="},
		{"clean input unchanged", "normal review text", "normal review text", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := v.SanitizeInput(tt.input)
			if tt.contains != "" && !strings.Contains(got, tt.contains) {
				t.Errorf("expected %q in output %q", tt.contains, got)
			}
			if tt.absent != "" && strings.Contains(got, tt.absent) {
				t.Errorf("expected %q absent from output %q", tt.absent, got)
			}
		})
	}
}

func TestValidateEnvironment(t *testing.T) {
	v := testValidator()

	t.Run("missing AI_PROVIDER_KEY", func(t *testing.T) {
		t.Setenv("CI_PIPELINE_SOURCE", "merge_request_event")
		t.Setenv("AI_PROVIDER_KEY", "")
		t.Setenv("GITLAB_TOKEN", "token")
		t.Setenv("CI_PROJECT_ID", "123")
		t.Setenv("CI_MERGE_REQUEST_IID", "1")
		err := v.ValidateEnvironment()
		if err == nil || !strings.Contains(err.Error(), "AI_PROVIDER_KEY") {
			t.Fatalf("expected error mentioning AI_PROVIDER_KEY, got %v", err)
		}
	})

	t.Run("missing GITLAB_TOKEN", func(t *testing.T) {
		t.Setenv("CI_PIPELINE_SOURCE", "merge_request_event")
		t.Setenv("AI_PROVIDER_KEY", "key")
		t.Setenv("GITLAB_TOKEN", "")
		t.Setenv("CI_PROJECT_ID", "123")
		t.Setenv("CI_MERGE_REQUEST_IID", "1")
		err := v.ValidateEnvironment()
		if err == nil || !strings.Contains(err.Error(), "GITLAB_TOKEN") {
			t.Fatalf("expected error mentioning GITLAB_TOKEN, got %v", err)
		}
	})

	t.Run("cross-repo mode missing TARGET_PROJECT_ID", func(t *testing.T) {
		t.Setenv("CI_PIPELINE_SOURCE", "trigger")
		t.Setenv("AI_PROVIDER_KEY", "key")
		t.Setenv("GITLAB_TOKEN", "token")
		t.Setenv("TARGET_PROJECT_ID", "")
		t.Setenv("TARGET_MR_IID", "1")
		t.Setenv("SOURCE_BRANCH", "feat")
		t.Setenv("TARGET_BRANCH", "main")
		err := v.ValidateEnvironment()
		if err == nil || !strings.Contains(err.Error(), "TARGET_PROJECT_ID") {
			t.Fatalf("expected error mentioning TARGET_PROJECT_ID, got %v", err)
		}
	})

	t.Run("all required vars set local mode", func(t *testing.T) {
		t.Setenv("CI_PIPELINE_SOURCE", "merge_request_event")
		t.Setenv("AI_PROVIDER_KEY", "key")
		t.Setenv("GITLAB_TOKEN", "token")
		t.Setenv("CI_PROJECT_ID", "123")
		t.Setenv("CI_MERGE_REQUEST_IID", "1")
		if err := v.ValidateEnvironment(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}
