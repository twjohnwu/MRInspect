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
	RetryAttempts   int
	RetryDelayMs    int
	MaxRetryDelayMs int
	TimeoutMs       int
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

	SelfReflection bool

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
	key := os.Getenv("AI_PROVIDER_KEY")
	if key == "" {
		return Config{}, fmt.Errorf("AI_PROVIDER_KEY environment variable is required")
	}
	token := os.Getenv("GITLAB_TOKEN")
	if token == "" {
		return Config{}, fmt.Errorf("GITLAB_TOKEN environment variable is required")
	}

	provider := AIProvider(strings.ToLower(getEnv("AI_PROVIDER", "gemini")))
	if provider != ProviderAnthropic && provider != ProviderGemini && provider != ProviderOpenAI {
		return Config{}, fmt.Errorf("unsupported AI_PROVIDER %q: must be anthropic, gemini, or openai", provider)
	}

	projectsDir := getEnv("PROJECTS_DIR", "./projects")

	cfg := Config{
		AIProvider:    provider,
		AIProviderKey: key,
		GitLabToken:   token,
		GitLabAPIBase: getEnv("GITLAB_API_BASE", "https://gitlab.com/api/v4"),

		Providers: map[AIProvider]ProviderConfig{
			ProviderAnthropic: {
				Model:       getEnv("ANTHROPIC_MODEL", "claude-3-5-sonnet-20241022"),
				MaxTokens:   getEnvInt("ANTHROPIC_MAX_TOKENS", 4000),
				Temperature: 0.1,
			},
			ProviderGemini: {
				Model:       getEnv("GEMINI_MODEL", "gemini-2.5-pro"),
				MaxTokens:   getEnvInt("GEMINI_MAX_TOKENS", 8000),
				Temperature: 0.1,
			},
			ProviderOpenAI: {
				Model:       getEnv("OPENAI_MODEL", "gpt-5"),
				MaxTokens:   getEnvInt("OPENAI_MAX_TOKENS", 4000),
				Temperature: 0.1,
			},
		},

		SelfReflection: getEnv("IS_SELF_REFLECTION", "false") == "true",

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
			RetryAttempts:   getEnvInt("API_RETRY_ATTEMPTS", 3),
			RetryDelayMs:    getEnvInt("API_RETRY_DELAY_MS", 1000),
			MaxRetryDelayMs: getEnvInt("API_MAX_RETRY_DELAY_MS", 10000),
			TimeoutMs:       getEnvInt("API_TIMEOUT_MS", 30000),
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
