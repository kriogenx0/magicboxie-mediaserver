package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	ListenAddr string `yaml:"listen_addr"`
	MoviesDir  string `yaml:"movies_dir"`
	MusicDir   string `yaml:"music_dir"`
	DataDir    string `yaml:"data_dir"`

	Auth struct {
		PasswordHash string `yaml:"password_hash"`
	} `yaml:"auth"`

	TMDB struct {
		APIReadToken string `yaml:"api_read_token"`
	} `yaml:"tmdb"`

	Transcode struct {
		MaxConcurrentJobs int    `yaml:"max_concurrent_jobs"`
		Preset            string `yaml:"preset"`
		CRF               int    `yaml:"crf"`
		// Threads caps how many CPU cores ffmpeg uses. Fewer cores means a
		// slower transcode but a cooler Pi. 0 lets ffmpeg use them all.
		Threads int `yaml:"threads"`
		// PauseAboveCelsius suspends transcoding while the CPU is at or above
		// this temperature. It resumes 5 degrees below it, or after 2 minutes
		// anywhere under it. 0 disables.
		PauseAboveCelsius float64 `yaml:"pause_above_celsius"`
	} `yaml:"transcode"`
}

func defaults() Config {
	var c Config
	c.ListenAddr = ":8080"
	c.MoviesDir = "/content/movies"
	c.MusicDir = "/content/music"
	c.DataDir = "/var/lib/magicboxie"
	c.Transcode.MaxConcurrentJobs = 1
	c.Transcode.Preset = "medium"
	c.Transcode.CRF = 20
	c.Transcode.Threads = 2
	c.Transcode.PauseAboveCelsius = 65
	return c
}

// Load reads a YAML config file at path, applying defaults for any unset field.
func Load(path string) (*Config, error) {
	c := defaults()

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file %q: %w", path, err)
	}
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parsing config file %q: %w", path, err)
	}

	if c.MoviesDir == "" {
		return nil, fmt.Errorf("config: movies_dir must be set")
	}
	if c.DataDir == "" {
		return nil, fmt.Errorf("config: data_dir must be set")
	}

	return &c, nil
}
