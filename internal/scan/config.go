package scan

// Config mirrors what viper will unmarshal from the CLI layer.
// Keep it simple and unopinionated here.
type Config struct {
	Root    string      `mapstructure:"root" json:"root" yaml:"root"`
	Out     string      `mapstructure:"out" json:"out" yaml:"out"`
	Entries []EntrySpec `mapstructure:"entries" json:"entries" yaml:"entries"`
}

// EntrySpec is a discriminated union. The CLI layer will map these into real providers.
type EntrySpec struct {
	Type string `mapstructure:"type" json:"type" yaml:"type"`

	// rootsTs fields
	File     string `mapstructure:"file" json:"file" yaml:"file"`
	NameFrom string `mapstructure:"nameFrom" json:"nameFrom" yaml:"nameFrom"`

	// explicit fields
	Name string `mapstructure:"name" json:"name" yaml:"name"`
	Path string `mapstructure:"path" json:"path" yaml:"path"`
}
