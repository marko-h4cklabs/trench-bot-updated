package config

import (
	"log"
	"sync"

	"github.com/spf13/viper"
)

// AgentConfig defines the structure for individual agents
type AgentConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	URL     string `mapstructure:"url"`
}

// TelegramConfig defines the structure for Telegram-related configurations
type TelegramConfig struct {
	BotToken           string `mapstructure:"bot_token"`
	GroupID            int64  `mapstructure:"group_id"`
	ScannerLogsThread  int64  `mapstructure:"scanner_logs_thread"`
	PotentialCAThread  int64  `mapstructure:"potential_ca_thread"`
	SystemLogsThreadID int64  `mapstructure:"system_logs_thread_id"`
}

// Config defines the global configuration structure
type Config struct {
	App struct {
		Port        string `mapstructure:"port"`
		Environment string `mapstructure:"environment"`
	} `mapstructure:"app"`

	Logging struct {
		Level string `mapstructure:"level"`
	} `mapstructure:"logging"`

	Database struct {
		URI  string `mapstructure:"uri"`
		Name string `mapstructure:"name"`
	} `mapstructure:"database"`

	Response struct {
		Provider  string `mapstructure:"provider"`
		ModelName string `mapstructure:"model_name"`
	} `mapstructure:"response"`

	Agents map[string]AgentConfig `mapstructure:"agents"`

	Neo4j struct {
		URI      string `mapstructure:"uri"`
		Username string `mapstructure:"username"`
		Password string `mapstructure:"password"`
	} `mapstructure:"neo4j"`

	Telegram TelegramConfig `mapstructure:"telegram"` // Added Telegram configuration
}

var (
	globalConfig *Config
	configLock   sync.RWMutex
)

// LoadConfig loads configuration from the specified file path and merges it with environment variables
func LoadConfig(path string) (*Config, error) {
	log.Printf("Starting to load configuration from file: %s", path)

	viper.SetConfigFile(path)   // Set the configuration file path
	viper.SetConfigType("yaml") // Explicitly specify the file type
	viper.AutomaticEnv()        // Allow environment variable overrides

	// Bind environment variables to the expected keys
	viper.SetEnvPrefix("APP") // Prefix for environment variables
	viper.BindEnv("app.port", "PORT")
	viper.BindEnv("app.environment", "ENVIRONMENT")
	viper.BindEnv("neo4j.uri", "NEO4J_URI")
	viper.BindEnv("neo4j.username", "NEO4J_USERNAME")
	viper.BindEnv("neo4j.password", "NEO4J_PASSWORD")

	// Bind Telegram environment variables
	viper.BindEnv("telegram.bot_token", "TELEGRAM_BOT_TOKEN")
	viper.BindEnv("telegram.group_id", "TELEGRAM_GROUP_ID")
	viper.BindEnv("telegram.scanner_logs_thread", "SCANNER_LOGS_THREAD_ID")
	viper.BindEnv("telegram.potential_ca_thread", "POTENTIAL_CA_THREAD_ID")
	viper.BindEnv("telegram.system_logs_thread_id", "SYSTEM_LOGS_THREAD_ID")

	var cfg Config

	// Read from the configuration file
	if err := viper.ReadInConfig(); err != nil {
		log.Printf("Warning: Could not read config file: %v", err)
	}

	// Unmarshal the configuration
	if err := viper.Unmarshal(&cfg); err != nil {
		log.Printf("Error unmarshalling configuration: %v", err)
		return nil, err
	}

	// Log the loaded configuration file for debugging
	log.Printf("Loaded configuration from file: %s", path)
	log.Printf("Configuration before environment overrides: %+v", cfg)

	// Log overrides from environment variables
	for _, key := range viper.AllKeys() {
		log.Printf("Config key: %s, Value: %v (from environment or file)", key, viper.Get(key))
	}

	return &cfg, nil
}

// SetGlobalConfig sets the loaded configuration globally
func SetGlobalConfig(cfg *Config) {
	configLock.Lock()
	defer configLock.Unlock()
	globalConfig = cfg
	log.Printf("Global configuration set successfully: %+v", globalConfig)
}

// GetGlobalConfig retrieves the globally set configuration
func GetGlobalConfig() *Config {
	configLock.RLock()
	defer configLock.RUnlock()
	if globalConfig == nil {
		log.Println("GetGlobalConfig: Global configuration is nil.")
	} else {
		log.Printf("GetGlobalConfig: Retrieved global configuration: %+v", globalConfig)
	}
	return globalConfig
}

// GetAgentConfig retrieves the configuration for a specific agent by name.
func GetAgentConfig(agentName string) (AgentConfig, bool) {
	globalConfig := GetGlobalConfig()

	// Check if the global configuration is nil
	if globalConfig == nil {
		log.Println("GetAgentConfig: Global configuration is nil. Ensure that LoadConfig is called before using GetAgentConfig.")
		return AgentConfig{}, false
	}

	// Debug log for retrieved global configuration
	log.Printf("GetAgentConfig: Retrieved global configuration: %+v", globalConfig)

	// Look for the specific agent in the configuration
	agentConfig, exists := globalConfig.Agents[agentName]
	if !exists {
		log.Printf("GetAgentConfig: Agent %s not found in configuration.", agentName)
	} else {
		log.Printf("GetAgentConfig: Found configuration for agent %s: %+v", agentName, agentConfig)
	}

	return agentConfig, exists
}
