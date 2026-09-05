package ragcmd

import (
	"os"
	"path/filepath"
	"testing"

	"mrinspect/internal/config"
)

func TestSystemDirectory_MapsServiceViaRegistry(t *testing.T) {
	projectsDir := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(projectsDir, "registry.yaml"),
		[]byte("defaultSystem: dir-a\nservices:\n  svc-a: dir-a\n"),
		0o600,
	); err != nil {
		t.Fatalf("write registry: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(projectsDir, "dir-a"), 0o755); err != nil {
		t.Fatalf("create system directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectsDir, "dir-a", "system.yaml"), []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write system project: %v", err)
	}

	cfg := config.Config{
		Service: config.ServiceConfig{Name: "svc-a", Type: "backend"},
		Projects: config.ProjectsConfig{
			Directory:    projectsDir,
			RegistryFile: filepath.Join(projectsDir, "registry.yaml"),
			SharedDir:    filepath.Join(projectsDir, "_shared"),
		},
	}

	got, err := SystemDirectory(cfg)
	if err != nil {
		t.Fatalf("SystemDirectory() error = %v", err)
	}
	if got != "dir-a" {
		t.Errorf("SystemDirectory() = %q, want %q", got, "dir-a")
	}

	cfg.Service.Name = "unmapped-service"
	cfg.Projects = config.ProjectsConfig{
		Directory:    filepath.Join(projectsDir, "missing"),
		RegistryFile: filepath.Join(projectsDir, "missing", "registry.yaml"),
		SharedDir:    filepath.Join(projectsDir, "missing", "_shared"),
	}
	got, err = SystemDirectory(cfg)
	if err != nil {
		t.Fatalf("SystemDirectory() unavailable loader error = %v", err)
	}
	if got != "unmapped-service" {
		t.Errorf("SystemDirectory() unavailable loader = %q, want %q", got, "unmapped-service")
	}
}
