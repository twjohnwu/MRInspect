package config

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

func TestEnvExampleMatchesUserSettableVariables(t *testing.T) {
	canonical := []string{
		"AI_PER_CALL_TIMEOUT_MS",
		"AI_PROVIDER",
		"AI_PROVIDER_KEY",
		"AI_RETRY_ATTEMPTS",
		"ANTHROPIC_API_KEY",
		"ANTHROPIC_MAX_TOKENS",
		"ANTHROPIC_MODEL",
		"API_MAX_RETRY_DELAY_MS",
		"API_RETRY_ATTEMPTS",
		"API_RETRY_DELAY_MS",
		"API_TIMEOUT_MS",
		"GEMINI_MAX_TOKENS",
		"GEMINI_MODEL",
		"GITLAB_API_BASE",
		"GITLAB_TOKEN",
		"IS_SELF_REFLECTION",
		"LOG_LEVEL",
		"MAX_FILES_CHANGED",
		"MRI_DIFF_PROMPT_SHARE",
		"MRI_LANE_CONCURRENCY",
		"MRI_MODEL_LIMITS",
		"MRI_PROMPT_BUDGET_FACTOR",
		"MRI_RAG_ON_NORMATIVE_EVICTION",
		"MRI_RAG_PACKAGE_VERSION",
		"MRI_RAG_SOURCE",
		"MRI_RAG_STORE",
		"MRI_REVIEW_DUMP_ENABLED",
		"MRI_REVIEW_MODE",
		"MRI_SERVICE_NAME",
		"MRI_SERVICE_TYPE",
		"OPENAI_MAX_TOKENS",
		"OPENAI_MODEL",
		"PROJECTS_DIR",
	}

	// Excluded CI variables are injected by GitLab or the cross-repository trigger contract,
	// rather than being user-settable application configuration.
	// AI_REVIEW_METRICS_FILE is a debug/evaluation output path, not production configuration.
	// MRI_DAILY_TOKEN_BUDGET and MRI_EVAL_ALLOW_CI apply only to the offline eval command.
	// MRI_RAG_BACKEND, MRI_RAG_ENABLED, and embedding switches are internal extension controls;
	// sqlite is the only production backend, so users should not configure them.
	// MRINSPECT_IMAGE_TAG belongs to the reusable CI template and is absent from this example.
	excluded := map[string]struct{}{
		"AI_REVIEW_METRICS_FILE":              {},
		"CI":                                  {},
		"CI_COMMIT_REF_NAME":                  {},
		"CI_MERGE_REQUEST_IID":                {},
		"CI_MERGE_REQUEST_TARGET_BRANCH_NAME": {},
		"CI_PIPELINE_SOURCE":                  {},
		"CI_PROJECT_ID":                       {},
		"MRI_MR_IID":                          {},
		"MRI_PROJECT_ID":                      {},
		"MRI_SOURCE_BRANCH":                   {},
		"MRI_TARGET_BRANCH":                   {},
	}

	path := filepath.Join("..", "..", ".env.example")
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer file.Close()

	envLine := regexp.MustCompile(`^\s*#?\s*([A-Z][A-Z0-9_]*)=`)
	found := make(map[string]struct{})
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if match := envLine.FindStringSubmatch(scanner.Text()); match != nil {
			found[match[1]] = struct{}{}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	wanted := make(map[string]struct{}, len(canonical))
	var missing []string
	for _, name := range canonical {
		wanted[name] = struct{}{}
		if _, ok := found[name]; !ok {
			missing = append(missing, name)
		}
	}

	var unexpected []string
	for name := range found {
		if _, ok := wanted[name]; ok {
			continue
		}
		if _, ok := excluded[name]; ok {
			continue
		}
		unexpected = append(unexpected, name)
	}
	sort.Strings(missing)
	sort.Strings(unexpected)

	if len(missing) != 0 || len(unexpected) != 0 {
		t.Fatalf(".env.example drift:\n  missing: %s\n  unexpected: %s",
			formatEnvNames(missing), formatEnvNames(unexpected))
	}
}

func formatEnvNames(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}
