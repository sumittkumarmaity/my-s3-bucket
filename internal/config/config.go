package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server struct {
		Host      string `yaml:"host"`
		S3Port    int    `yaml:"s3_port"`
		AdminPort int    `yaml:"admin_port"`
	} `yaml:"server"`

	Storage struct {
		Root                string  `yaml:"root"`
		MaxObjectSize       int64   `yaml:"max_object_size"`
		MinFreeSpaceBytes   int64   `yaml:"min_free_space_bytes"`
		MinFreeSpacePercent float64 `yaml:"min_free_space_percent"`
	} `yaml:"storage"`

	Metadata struct {
		Driver string `yaml:"driver"`
		Path   string `yaml:"path"`
	} `yaml:"metadata"`

	Security struct {
		AuthEnabled    bool   `yaml:"auth_enabled"`
		AdminAccessKey string `yaml:"admin_access_key"`
		AdminSecretKey string `yaml:"admin_secret_key"`
		AdminToken     string `yaml:"admin_token"`
		EncryptionKey  string `yaml:"encryption_key"`
	} `yaml:"security"`

	Replication struct {
		Enabled           bool     `yaml:"enabled"`
		NodeID            string   `yaml:"node_id"`
		ListenAddress     string   `yaml:"listen_address"`
		AuthToken         string   `yaml:"auth_token"`
		PeerNodes         []string `yaml:"peer_nodes"`
		PollIntervalSec   int      `yaml:"poll_interval_sec"`
	} `yaml:"replication"`

	Jobs struct {
		GCIntervalSeconds            int `yaml:"gc_interval_seconds"`
		IntegrityScanIntervalSeconds int `yaml:"integrity_scan_interval_seconds"`
	} `yaml:"jobs"`
}

// DefaultConfig returns default values.
func DefaultConfig() *Config {
	cfg := &Config{}
	cfg.Server.Host = "0.0.0.0"
	cfg.Server.S3Port = 9000
	cfg.Server.AdminPort = 9001

	cfg.Storage.Root = "./data/storage"
	cfg.Storage.MaxObjectSize = 5 * 1024 * 1024 * 1024 // 5GB
	cfg.Storage.MinFreeSpaceBytes = 10 * 1024 * 1024 * 1024 // 10GB
	cfg.Storage.MinFreeSpacePercent = 5.0 // 5%

	cfg.Metadata.Driver = "sqlite"
	cfg.Metadata.Path = "./data/metadata/storage.db"

	cfg.Security.AuthEnabled = true
	cfg.Security.AdminAccessKey = "storageadmin"
	cfg.Security.AdminSecretKey = "storageadminsecretkey123"
	cfg.Security.AdminToken = "storageadminsecretkey123"
	cfg.Security.EncryptionKey = "" // disabled by default

	cfg.Replication.Enabled = false
	cfg.Replication.NodeID = "node-1"
	cfg.Replication.ListenAddress = "http://127.0.0.1:9001"
	cfg.Replication.AuthToken = "rep-secret-token"
	cfg.Replication.PollIntervalSec = 5

	cfg.Jobs.GCIntervalSeconds = 3600             // 1 hour
	cfg.Jobs.IntegrityScanIntervalSeconds = 86400 // 24 hours

	return cfg
}

func LoadConfig(path string) (*Config, error) {
	cfg := DefaultConfig()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, err
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}

	if cfg.Security.AdminToken == "" && cfg.Security.AdminSecretKey != "" {
		cfg.Security.AdminToken = cfg.Security.AdminSecretKey
	}

	if cfg.Storage.MinFreeSpaceBytes <= 0 {
		cfg.Storage.MinFreeSpaceBytes = 10 * 1024 * 1024 * 1024
	}
	if cfg.Storage.MinFreeSpacePercent <= 0 {
		cfg.Storage.MinFreeSpacePercent = 5.0
	}

	return cfg, nil
}
