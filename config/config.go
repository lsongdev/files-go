package config

type Config struct {
	TMDB      TMDB      `yaml:"tmdb"`
	Libraries []Library `yaml:"libraries"`
}

type Library struct {
	Name string `yaml:"name"`
	Path string `yaml:"path"`
	Slug string `yaml:"slug"`
}

type TMDB struct {
	APIKey string `yaml:"api_key"`
}

func (c *Config) FindLibraryIndex(name string) int {
	for index, library := range c.Libraries {
		if library.Name == name {
			return index
		}
	}
	return -1
}

func (c *Config) FindLibraryBySlug(slug string) int {
	for index, library := range c.Libraries {
		if library.Slug == slug {
			return index
		}
	}
	return -1
}

func (c *Config) GetLibrarySlug(index int) string {
	if index >= 0 && index < len(c.Libraries) {
		return c.Libraries[index].Slug
	}
	return ""
}
