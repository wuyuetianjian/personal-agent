package skill

import (
	"gopkg.in/yaml.v3"
	"os"
)

func Load(path string) (Manifest, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, err
	}
	var manifest Manifest
	if err := yaml.Unmarshal(b, &manifest); err != nil {
		return Manifest{}, err
	}
	return Normalize(manifest), nil
}
