package project

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
	"mrinspect/internal/config"
)

type Loader struct {
	projectsDir  string
	registryFile string
	sharedDir    string
}

func NewLoader(cfg config.ProjectsConfig) *Loader {
	return &Loader{
		projectsDir:  cfg.Directory,
		registryFile: cfg.RegistryFile,
		sharedDir:    cfg.SharedDir,
	}
}

func (l *Loader) IsAvailable() bool {
	_, err1 := os.Stat(l.projectsDir)
	_, err2 := os.Stat(l.registryFile)
	return err1 == nil && err2 == nil
}

func (l *Loader) LoadProfile(serviceName, serviceType string) (LoadedProject, error) {
	registry, err := l.loadRegistry()
	if err != nil {
		return LoadedProject{}, fmt.Errorf("LoadProfile: registry: %w", err)
	}

	systemName := registry.DefaultSystem
	if systemName == "" {
		systemName = "default"
	}
	if mapped, ok := registry.Services[serviceName]; ok {
		systemName = mapped
	}

	sys, err := l.loadSystemProject(systemName)
	if err != nil {
		return LoadedProject{}, fmt.Errorf("LoadProfile: system project: %w", err)
	}

	resolvedType := l.resolveServiceType(serviceName, sys, serviceType)
	shared, system := l.loadDocs(systemName, resolvedType)

	return LoadedProject{
		SystemDirectory:     systemName,
		System:              sys,
		SharedDocContents:   shared,
		SystemDocContents:   system,
		ResolvedServiceType: resolvedType,
	}, nil
}

func (l *Loader) loadRegistry() (ServiceRegistry, error) {
	data, err := os.ReadFile(l.registryFile)
	if err != nil {
		return ServiceRegistry{}, err
	}
	var reg ServiceRegistry
	if err := yaml.Unmarshal(data, &reg); err != nil {
		return ServiceRegistry{}, err
	}
	if reg.Services == nil {
		reg.Services = map[string]string{}
	}
	return reg, nil
}

func (l *Loader) loadSystemProject(systemName string) (SystemProject, error) {
	path := filepath.Join(l.projectsDir, systemName, "system.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return SystemProject{}, err
	}
	var sys SystemProject
	if err := yaml.Unmarshal(data, &sys); err != nil {
		return SystemProject{}, err
	}
	return sys, nil
}

func (l *Loader) resolveServiceType(serviceName string, sys SystemProject, envType string) string {
	if override, ok := sys.ServiceTypeOverrides[serviceName]; ok {
		return override
	}
	if envType != "" {
		return envType
	}
	if sys.DefaultServiceType != "" {
		return sys.DefaultServiceType
	}
	return "backend"
}

func (l *Loader) loadDocs(systemName, serviceType string) (shared, system []DocFile) {
	shared = append(shared, l.loadMDFiles(l.sharedDir)...)
	shared = append(shared, l.loadMDFiles(filepath.Join(l.sharedDir, serviceType))...)
	system = l.loadMDFiles(filepath.Join(l.projectsDir, systemName))
	return shared, system
}

func (l *Loader) loadMDFiles(dirPath string) []DocFile {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return nil
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)

	var docs []DocFile
	for _, name := range files {
		data, err := os.ReadFile(filepath.Join(dirPath, name))
		if err != nil {
			continue
		}
		docs = append(docs, DocFile{Filename: name, Content: string(data)})
	}
	return docs
}
