package project

type SystemProject struct {
	Name                 string            `yaml:"name"`
	Description          string            `yaml:"description"`
	DefaultServiceType   string            `yaml:"defaultServiceType"`
	Frameworks           []string          `yaml:"frameworks"`
	ServiceTypeOverrides map[string]string `yaml:"serviceTypeOverrides"`
}

type ServiceRegistry struct {
	DefaultSystem string            `yaml:"defaultSystem"`
	Services      map[string]string `yaml:"services"`
}

type DocFile struct {
	Filename string
	Content  string
}

type LoadedProject struct {
	System              SystemProject
	SharedDocContents   []DocFile
	SystemDocContents   []DocFile
	ResolvedServiceType string
}
