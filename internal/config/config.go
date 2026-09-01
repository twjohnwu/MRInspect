package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

type AIProvider string

const (
	ProviderAnthropic AIProvider = "anthropic"
	ProviderGemini    AIProvider = "gemini"
	ProviderOpenAI    AIProvider = "openai"
)

type ProviderConfig struct {
	Model       string
	MaxTokens   int
	Temperature float64
}

type ServiceConfig struct {
	Name string
	Type string
}

type ProjectsConfig struct {
	Directory    string
	RegistryFile string
	SharedDir    string
}

type APIConfig struct {
	RetryAttempts    int
	RetryDelayMs     int
	MaxRetryDelayMs  int
	TimeoutMs        int
	PerCallTimeoutMs int
	AILogDir         string
}

type ValidationConfig struct {
	AllowedExtensions []string
	MaxFilesChanged   int
	MaxDiffSizeKB     float64
	AIRetryAttempts   int
}

type ErrorReportingConfig struct {
	Enabled               bool
	PostToMR              bool
	MaxErrorMessageLength int
}

type Config struct {
	AIProvider    AIProvider
	AIProviderKey string
	GitLabToken   string
	GitLabAPIBase string

	Providers map[AIProvider]ProviderConfig

	SelfReflection    bool
	ReviewMode        string
	ReviewDumpEnabled bool

	RAGOnNormativeEviction string
	LaneConcurrency        string
	LaneConcurrencySet     bool
	DiffPromptShare        string
	ModelLimits            string

	Service   ServiceConfig
	CrossRepo struct {
		Enabled bool
	}

	Projects   ProjectsConfig
	API        APIConfig
	Validation ValidationConfig

	MetricsFile    string
	LogLevel       string
	ErrorReporting ErrorReportingConfig
}

func Load() (Config, error) {
	return load(true)
}

// LoadForEval loads only the configuration required by offline evaluation.
// It intentionally skips the GitLab credential requirement because the eval
// path consumes fixture-owned merge request data and never calls GitLab.
func LoadForEval() (Config, error) {
	return load(false)
}

func load(requireGitLabToken bool) (Config, error) {
	key := os.Getenv("AI_PROVIDER_KEY")
	if key == "" {
		return Config{}, fmt.Errorf("AI_PROVIDER_KEY environment variable is required")
	}
	token := os.Getenv("GITLAB_TOKEN")
	if requireGitLabToken && token == "" {
		return Config{}, fmt.Errorf("GITLAB_TOKEN environment variable is required")
	}

	provider := AIProvider(strings.ToLower(getEnv("AI_PROVIDER", "openai")))
	if provider != ProviderAnthropic && provider != ProviderGemini && provider != ProviderOpenAI {
		return Config{}, fmt.Errorf("unsupported AI_PROVIDER %q: must be anthropic, gemini, or openai", provider)
	}

	projectsDir := getEnv("PROJECTS_DIR", "./projects")
	laneConcurrency, laneConcurrencySet := os.LookupEnv("MRI_LANE_CONCURRENCY")

	cfg := Config{
		AIProvider:    provider,
		AIProviderKey: key,
		GitLabToken:   token,
		GitLabAPIBase: getEnv("GITLAB_API_BASE", "https://gitlab.com/api/v4"),

		Providers: map[AIProvider]ProviderConfig{
			ProviderAnthropic: {
				Model:       getEnv("ANTHROPIC_MODEL", "claude-sonnet-5"),
				MaxTokens:   getEnvInt("ANTHROPIC_MAX_TOKENS", 4000),
				Temperature: 0.1,
			},
			ProviderGemini: {
				Model:       getEnv("GEMINI_MODEL", "gemini-3.1-pro-preview"),
				MaxTokens:   getEnvInt("GEMINI_MAX_TOKENS", 8000),
				Temperature: 0.1,
			},
			ProviderOpenAI: {
				Model:       getEnv("OPENAI_MODEL", "gpt-5.6"),
				MaxTokens:   getEnvInt("OPENAI_MAX_TOKENS", 4000),
				Temperature: 0.1,
			},
		},

		SelfReflection:         getEnv("IS_SELF_REFLECTION", "false") == "true",
		ReviewMode:             getEnv("MRI_REVIEW_MODE", "single"),
		ReviewDumpEnabled:      getEnv("MRI_REVIEW_DUMP_ENABLED", "false") == "true",
		RAGOnNormativeEviction: getEnv("MRI_RAG_ON_NORMATIVE_EVICTION", "warn"),
		LaneConcurrency:        laneConcurrency,
		LaneConcurrencySet:     laneConcurrencySet,
		DiffPromptShare:        os.Getenv("MRI_DIFF_PROMPT_SHARE"),
		ModelLimits:            os.Getenv("MRI_MODEL_LIMITS"),

		Service: ServiceConfig{
			Name: getEnv("MRI_SERVICE_NAME", "unknown"),
			Type: getEnv("MRI_SERVICE_TYPE", "backend"),
		},

		CrossRepo: struct{ Enabled bool }{
			Enabled: os.Getenv("CI_PIPELINE_SOURCE") == "trigger",
		},

		Projects: ProjectsConfig{
			Directory:    projectsDir,
			RegistryFile: projectsDir + "/registry.yaml",
			SharedDir:    projectsDir + "/_shared",
		},

		API: APIConfig{
			RetryAttempts:    getEnvInt("API_RETRY_ATTEMPTS", 3),
			RetryDelayMs:     getEnvInt("API_RETRY_DELAY_MS", 1000),
			MaxRetryDelayMs:  getEnvInt("API_MAX_RETRY_DELAY_MS", 10000),
			TimeoutMs:        getEnvInt("API_TIMEOUT_MS", 30000),
			PerCallTimeoutMs: getEnvInt("AI_PER_CALL_TIMEOUT_MS", 120000),
			AILogDir:         os.Getenv("MRI_AI_LOG_DIR"),
		},

		Validation: ValidationConfig{
			AllowedExtensions: []string{
				".ts", ".js", ".tsx", ".jsx", ".py", ".go",
				".tf", ".hcl", ".json", ".yaml", ".yml", ".md",
				".sql", ".sh", ".bash", ".env.example",
			},
			MaxFilesChanged: getEnvInt("MAX_FILES_CHANGED", 50),
			MaxDiffSizeKB:   300,
			AIRetryAttempts: getEnvInt("AI_RETRY_ATTEMPTS", 3),
		},

		MetricsFile: getEnv("AI_REVIEW_METRICS_FILE", "./mrinspect-metrics.json"),
		LogLevel:    getEnv("LOG_LEVEL", "info"),

		ErrorReporting: ErrorReportingConfig{
			Enabled:               true,
			PostToMR:              true,
			MaxErrorMessageLength: 2000,
		},
	}

	return cfg, nil
}

// LoadForIndex loads only the configuration required by mrinspect index
// (REQ-05). Unlike Load, it must not require review credentials.
func LoadForIndex() (Config, error) {
	projectsDir := getEnv("PROJECTS_DIR", "./projects")
	return Config{
		Service: ServiceConfig{
			Name: getEnv("MRI_SERVICE_NAME", "unknown"),
			Type: getEnv("MRI_SERVICE_TYPE", "backend"),
		},
		Projects: ProjectsConfig{
			Directory:    projectsDir,
			RegistryFile: projectsDir + "/registry.yaml",
			SharedDir:    projectsDir + "/_shared",
		},
	}, nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}
