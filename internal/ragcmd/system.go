package ragcmd

import (
	"fmt"

	"mrinspect/internal/config"
	"mrinspect/internal/project"
)

func SystemDirectory(cfg config.Config) (string, error) {
	loader := project.NewLoader(cfg.Projects)
	if !loader.IsAvailable() {
		return cfg.Service.Name, nil
	}

	profile, err := loader.LoadProfile(cfg.Service.Name, cfg.Service.Type)
	if err != nil {
		return "", fmt.Errorf("system directory: %w", err)
	}
	return profile.SystemDirectory, nil
}
