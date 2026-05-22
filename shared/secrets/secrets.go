package secrets

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"sync"
)

// AppSecrets contains all application secrets loaded from Docker secrets or environment variables.
// In production, secrets are loaded from a combined .env-style file at /run/secrets/app_secrets.
// In development, they fall back to environment variables.
type AppSecrets struct {
	Env    string
	Domain string

	BackendPort string

	DatabaseHost     string
	DatabasePort     string
	DatabaseUsername string
	DatabasePassword string
	DatabaseName     string

	AdminUsername       string
	AdminPasswordBcrypt string
	AdminJWTSecret      string

	VoterSalt string
}

var (
	instance        *AppSecrets
	once            sync.Once
	loadErr         error
	secretsFromFile map[string]string
)

const dockerSecretsFile = "/run/secrets/app_secrets"

func Load() (*AppSecrets, error) {
	once.Do(func() {
		instance, loadErr = loadSecrets()
	})
	return instance, loadErr
}

func Get() *AppSecrets {
	if instance == nil {
		panic("secrets not loaded - call secrets.Load() first")
	}
	return instance
}

func MustLoad() *AppSecrets {
	s, err := Load()
	if err != nil {
		panic(fmt.Sprintf("failed to load secrets: %v", err))
	}
	return s
}

func loadSecrets() (*AppSecrets, error) {
	secretsFromFile = loadSecretsFile(dockerSecretsFile)

	s := &AppSecrets{}
	s.Env = getEnvOrDefault("ENV", "development")
	s.Domain = loadSecret("DOMAIN", "localhost")
	s.BackendPort = loadSecret("BACKEND_PORT", "8104")

	s.DatabaseHost = loadSecret("DATABASE_HOST", "localhost")
	s.DatabasePort = loadSecret("DATABASE_PORT", "5432")
	s.DatabaseUsername = loadSecret("DATABASE_USERNAME", "")
	s.DatabasePassword = loadSecret("DATABASE_PASSWORD", "")
	s.DatabaseName = loadSecret("DATABASE_NAME", "")

	s.AdminUsername = loadSecret("ADMIN_USERNAME", "")
	s.AdminPasswordBcrypt = loadSecret("ADMIN_PASSWORD_BCRYPT", "")
	s.AdminJWTSecret = loadSecret("ADMIN_JWT_SECRET", "")

	s.VoterSalt = loadSecret("VOTER_SALT", "")

	if err := s.validate(); err != nil {
		return nil, err
	}
	return s, nil
}

func loadSecretsFile(path string) map[string]string {
	result := make(map[string]string)
	file, err := os.Open(path)
	if err != nil {
		return result
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') ||
				(value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}
		result[key] = value
	}
	return result
}

func loadSecret(name, defaultValue string) string {
	if v, ok := secretsFromFile[name]; ok && v != "" {
		return v
	}
	if v := os.Getenv(name); v != "" {
		return v
	}
	return defaultValue
}

func getEnvOrDefault(name, defaultValue string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return defaultValue
}

func (s *AppSecrets) validate() error {
	var missing []string
	if s.DatabaseUsername == "" {
		missing = append(missing, "DATABASE_USERNAME")
	}
	if s.DatabasePassword == "" {
		missing = append(missing, "DATABASE_PASSWORD")
	}
	if s.DatabaseName == "" {
		missing = append(missing, "DATABASE_NAME")
	}
	if s.AdminUsername == "" {
		missing = append(missing, "ADMIN_USERNAME")
	}
	if s.AdminPasswordBcrypt == "" {
		missing = append(missing, "ADMIN_PASSWORD_BCRYPT")
	}
	if s.AdminJWTSecret == "" {
		missing = append(missing, "ADMIN_JWT_SECRET")
	}
	if s.VoterSalt == "" {
		missing = append(missing, "VOTER_SALT")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required secrets: %s", strings.Join(missing, ", "))
	}
	return nil
}

func (s *AppSecrets) IsProduction() bool  { return s.Env == "production" }
func (s *AppSecrets) IsDevelopment() bool { return s.Env == "development" }
