package config

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Addr             string
	DBDriver         string // sqlite | postgres | mysql
	DBDSN            string
	JWTSecret        string
	JWTIssuer        string
	StaticUsers      string // GOATFLOW_STATIC_USERS: "alice:pass:SystemAdmin,bob:pass:Engineer"
	LDAPEnabled      bool
	SMTPHost         string
	SMTPPort         int
	SMTPFrom         string
	SMTPUser         string
	SMTPPass         string
	WorkerPoolSize   int
	WorkerPollMillis int
	LogLevel         string // fatal|error|warning|info|debug
}

// fileConfig mirrors Config with a friendlier YAML shape (nested db/smtp/worker
// blocks) so a config file reads naturally; see config.example.yaml.
type fileConfig struct {
	Addr        *string `yaml:"addr"`
	LogLevel    *string `yaml:"log_level"`
	StaticUsers *string `yaml:"static_users"`
	LDAPEnabled *bool   `yaml:"ldap_enabled"`

	DB *struct {
		Driver *string `yaml:"driver"`
		DSN    *string `yaml:"dsn"`
	} `yaml:"db"`

	JWT *struct {
		Secret *string `yaml:"secret"`
		Issuer *string `yaml:"issuer"`
	} `yaml:"jwt"`

	SMTP *struct {
		Host *string `yaml:"host"`
		Port *int    `yaml:"port"`
		From *string `yaml:"from"`
		User *string `yaml:"user"`
		Pass *string `yaml:"pass"`
	} `yaml:"smtp"`

	Worker *struct {
		PoolSize *int `yaml:"pool_size"`
		PollMs   *int `yaml:"poll_ms"`
	} `yaml:"worker"`
}

// Load builds the process config from, in increasing priority: hardcoded
// defaults, a YAML file (-config flag or SERVICEDESK_CONFIG_FILE env var),
// then individual environment variables (so a mounted k8s ConfigMap can
// still be overridden ad hoc without editing the file).
func Load() Config {
	f := loadFileConfig()

	c := Config{
		Addr:             getEnv("SERVICEDESK_ADDR", fromPtr(f.Addr, ":8080")),
		DBDriver:         getEnv("SERVICEDESK_DB_DRIVER", dbStrField(f, "driver", "sqlite")),
		DBDSN:            getEnv("SERVICEDESK_DB_DSN", dbStrField(f, "dsn", "file:servicedesk.db?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)")),
		JWTSecret:        getEnv("SERVICEDESK_JWT_SECRET", jwtField(f, "secret", "dev-insecure-secret-change-me")),
		JWTIssuer:        getEnv("SERVICEDESK_JWT_ISSUER", jwtField(f, "issuer", "servicedesk")),
		StaticUsers:      getEnv("GOATFLOW_STATIC_USERS", fromPtr(f.StaticUsers, "")),
		LDAPEnabled:      getEnvBool("LDAP_ENABLED", fromBoolPtr(f.LDAPEnabled, false)),
		SMTPHost:         getEnv("SERVICEDESK_SMTP_HOST", smtpStrField(f, "host", "")),
		SMTPPort:         getEnvInt("SERVICEDESK_SMTP_PORT", smtpIntField(f, "port", 587)),
		SMTPFrom:         getEnv("SERVICEDESK_SMTP_FROM", smtpStrField(f, "from", "servicedesk@example.com")),
		SMTPUser:         getEnv("SERVICEDESK_SMTP_USER", smtpStrField(f, "user", "")),
		SMTPPass:         getEnv("SERVICEDESK_SMTP_PASS", smtpStrField(f, "pass", "")),
		WorkerPoolSize:   getEnvInt("SERVICEDESK_WORKER_POOL_SIZE", workerIntField(f, "pool_size", 4)),
		WorkerPollMillis: getEnvInt("SERVICEDESK_WORKER_POLL_MS", workerIntField(f, "poll_ms", 500)),
		LogLevel:         getEnv("SERVICEDESK_LOG_LEVEL", fromPtr(f.LogLevel, "info")),
	}
	return c
}

// StaticUserEntries parses "user:pass:Role,user2:pass2:Role2" into tuples.
func (c Config) StaticUserEntries() [][3]string {
	var out [][3]string
	if strings.TrimSpace(c.StaticUsers) == "" {
		return out
	}
	for _, entry := range strings.Split(c.StaticUsers, ",") {
		parts := strings.SplitN(strings.TrimSpace(entry), ":", 3)
		if len(parts) != 3 {
			continue
		}
		out = append(out, [3]string{parts[0], parts[1], parts[2]})
	}
	return out
}

func configFilePath() string {
	fs := flag.NewFlagSet("servicedesk", flag.ContinueOnError)
	fs.SetOutput(nil)
	path := fs.String("config", "", "path to a YAML config file (see config.example.yaml)")
	_ = fs.Parse(os.Args[1:])
	if *path != "" {
		return *path
	}
	return os.Getenv("SERVICEDESK_CONFIG_FILE")
}

func loadFileConfig() fileConfig {
	var f fileConfig
	path := configFilePath()
	if path == "" {
		return f
	}
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: could not read %s: %v (continuing with env/defaults)\n", path, err)
		return f
	}
	if err := yaml.Unmarshal(data, &f); err != nil {
		fmt.Fprintf(os.Stderr, "config: could not parse %s: %v (continuing with env/defaults)\n", path, err)
		return fileConfig{}
	}
	return f
}

func fromPtr(p *string, fallback string) string {
	if p != nil {
		return *p
	}
	return fallback
}

func fromBoolPtr(p *bool, fallback bool) bool {
	if p != nil {
		return *p
	}
	return fallback
}

func dbStrField(f fileConfig, which, fallback string) string {
	if f.DB == nil {
		return fallback
	}
	switch which {
	case "driver":
		return fromPtr(f.DB.Driver, fallback)
	case "dsn":
		return fromPtr(f.DB.DSN, fallback)
	}
	return fallback
}

func jwtField(f fileConfig, which, fallback string) string {
	if f.JWT == nil {
		return fallback
	}
	switch which {
	case "secret":
		return fromPtr(f.JWT.Secret, fallback)
	case "issuer":
		return fromPtr(f.JWT.Issuer, fallback)
	}
	return fallback
}

func smtpStrField(f fileConfig, which, fallback string) string {
	if f.SMTP == nil {
		return fallback
	}
	switch which {
	case "host":
		return fromPtr(f.SMTP.Host, fallback)
	case "from":
		return fromPtr(f.SMTP.From, fallback)
	case "user":
		return fromPtr(f.SMTP.User, fallback)
	case "pass":
		return fromPtr(f.SMTP.Pass, fallback)
	}
	return fallback
}

func smtpIntField(f fileConfig, which string, fallback int) int {
	if f.SMTP == nil || which != "port" || f.SMTP.Port == nil {
		return fallback
	}
	return *f.SMTP.Port
}

func workerIntField(f fileConfig, which string, fallback int) int {
	if f.Worker == nil {
		return fallback
	}
	switch which {
	case "pool_size":
		if f.Worker.PoolSize != nil {
			return *f.Worker.PoolSize
		}
	case "poll_ms":
		if f.Worker.PollMs != nil {
			return *f.Worker.PollMs
		}
	}
	return fallback
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

func getEnvBool(key string, fallback bool) bool {
	if v, ok := os.LookupEnv(key); ok {
		return v == "true"
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v, ok := os.LookupEnv(key); ok {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}
