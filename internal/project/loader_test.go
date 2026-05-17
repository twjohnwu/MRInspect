package project

import (
	"path/filepath"
	"testing"

	"mrinspect/internal/config"
)

func newTestLoader(t *testing.T) *Loader {
	t.Helper()
	dir := filepath.Join("..", "..", "projects")
	return NewLoader(config.ProjectsConfig{
		Directory:    dir,
		RegistryFile: filepath.Join(dir, "registry.yaml"),
		SharedDir:    filepath.Join(dir, "_shared"),
	})
}

func TestIsAvailable(t *testing.T) {
	t.Run("real projects dir", func(t *testing.T) {
		l := newTestLoader(t)
		if !l.IsAvailable() {
			t.Error("expected IsAvailable() == true for real projects dir")
		}
	})

	t.Run("non-existent dir", func(t *testing.T) {
		l := NewLoader(config.ProjectsConfig{
			Directory:    "/nonexistent/path",
			RegistryFile: "/nonexistent/path/registry.yaml",
			SharedDir:    "/nonexistent/path/_shared",
		})
		if l.IsAvailable() {
			t.Error("expected IsAvailable() == false for non-existent dir")
		}
	})
}

func TestLoadProfile(t *testing.T) {
	l := newTestLoader(t)

	t.Run("margherita-pizza system", func(t *testing.T) {
		p, err := l.LoadProfile("dough-service", "backend")
		if err != nil {
			t.Fatalf("LoadProfile: %v", err)
		}
		if p.System.Name != "Margherita Pizza" {
			t.Errorf("System.Name: want %q, got %q", "Margherita Pizza", p.System.Name)
		}
		if p.ResolvedServiceType != "backend" {
			t.Errorf("ResolvedServiceType: want %q, got %q", "backend", p.ResolvedServiceType)
		}
		if !containsAll(p.System.Frameworks, "Go", "PostgreSQL") {
			t.Errorf("Frameworks missing Go or PostgreSQL: %v", p.System.Frameworks)
		}
		if len(p.SharedDocContents) == 0 {
			t.Error("SharedDocContents is empty; expected coding-standards.md to be loaded")
		}
		if len(p.SystemDocContents) == 0 {
			t.Error("SystemDocContents is empty; expected architecture.md + review-focus.md to be loaded")
		}
	})

	t.Run("fried-chicken system", func(t *testing.T) {
		p, err := l.LoadProfile("brine-service", "backend")
		if err != nil {
			t.Fatalf("LoadProfile: %v", err)
		}
		if p.System.Name != "Fried Chicken" {
			t.Errorf("System.Name: want %q, got %q", "Fried Chicken", p.System.Name)
		}
		if !containsAll(p.System.Frameworks, "Kafka", "MongoDB") {
			t.Errorf("Frameworks missing Kafka or MongoDB: %v", p.System.Frameworks)
		}
	})

	t.Run("service type override to frontend", func(t *testing.T) {
		p, err := l.LoadProfile("pizza-portal", "backend")
		if err != nil {
			t.Fatalf("LoadProfile: %v", err)
		}
		if p.ResolvedServiceType != "frontend" {
			t.Errorf("ResolvedServiceType: want %q, got %q", "frontend", p.ResolvedServiceType)
		}
	})

	t.Run("unknown service falls back to default system", func(t *testing.T) {
		p, err := l.LoadProfile("unknown-service", "backend")
		if err != nil {
			t.Fatalf("LoadProfile: %v", err)
		}
		if p.System.Name != "Margherita Pizza" {
			t.Errorf("System.Name: want %q, got %q", "Margherita Pizza", p.System.Name)
		}
	})

	t.Run("bad registry path returns error", func(t *testing.T) {
		bad := NewLoader(config.ProjectsConfig{
			Directory:    "/nonexistent",
			RegistryFile: "/nonexistent/registry.yaml",
			SharedDir:    "/nonexistent/_shared",
		})
		_, err := bad.LoadProfile("any-service", "backend")
		if err == nil {
			t.Error("expected error for non-existent registry")
		}
	})
}

func TestProfileStructure(t *testing.T) {
	l := newTestLoader(t)

	t.Run("registry has defaultSystem and services", func(t *testing.T) {
		reg, err := l.loadRegistry()
		if err != nil {
			t.Fatalf("loadRegistry: %v", err)
		}
		if reg.DefaultSystem == "" {
			t.Error("registry.DefaultSystem is empty")
		}
		if len(reg.Services) == 0 {
			t.Error("registry.Services map is empty")
		}
	})

	t.Run("margherita-pizza system.yaml required fields", func(t *testing.T) {
		sys, err := l.loadSystemProject("margherita-pizza")
		if err != nil {
			t.Fatalf("loadSystemProject: %v", err)
		}
		if sys.Name == "" {
			t.Error("Name is empty")
		}
		if sys.DefaultServiceType == "" {
			t.Error("DefaultServiceType is empty")
		}
		if len(sys.Frameworks) == 0 {
			t.Error("Frameworks is empty")
		}
	})

	t.Run("fried-chicken system.yaml required fields", func(t *testing.T) {
		sys, err := l.loadSystemProject("fried-chicken")
		if err != nil {
			t.Fatalf("loadSystemProject: %v", err)
		}
		if sys.Name == "" {
			t.Error("Name is empty")
		}
		if sys.DefaultServiceType == "" {
			t.Error("DefaultServiceType is empty")
		}
		if len(sys.Frameworks) == 0 {
			t.Error("Frameworks is empty")
		}
	})

	t.Run("shared docs are non-empty", func(t *testing.T) {
		p, err := l.LoadProfile("dough-service", "backend")
		if err != nil {
			t.Fatalf("LoadProfile: %v", err)
		}
		if len(p.SharedDocContents) == 0 {
			t.Fatal("no shared docs loaded")
		}
		for _, d := range p.SharedDocContents {
			if d.Content == "" {
				t.Errorf("shared doc %q has empty content", d.Filename)
			}
		}
	})

	t.Run("system docs are non-empty", func(t *testing.T) {
		p, err := l.LoadProfile("dough-service", "backend")
		if err != nil {
			t.Fatalf("LoadProfile: %v", err)
		}
		if len(p.SystemDocContents) == 0 {
			t.Fatal("no system docs loaded")
		}
		for _, d := range p.SystemDocContents {
			if d.Content == "" {
				t.Errorf("system doc %q has empty content", d.Filename)
			}
		}
	})
}

func containsAll(slice []string, items ...string) bool {
	set := make(map[string]bool, len(slice))
	for _, s := range slice {
		set[s] = true
	}
	for _, item := range items {
		if !set[item] {
			return false
		}
	}
	return true
}
