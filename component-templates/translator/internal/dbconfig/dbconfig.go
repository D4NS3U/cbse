// Package dbconfig carries the validated PostgreSQL connection fields the
// framework reads from the mounted Detail DB and Result DB connection Secrets.
// It is a standalone type shared by the config, database-endpoint, and
// generator packages so none of them depend on the others for the type.
package dbconfig

import "fmt"

// DatabaseConfig is the exact set of PostgreSQL connection fields the framework
// reads from a mounted connection Secret: host, port, dbname, user, and
// password. The framework always sets sslmode=disable when connecting; the
// Secret carries no sslmode field.
type DatabaseConfig struct {
	Host     string
	Port     int
	DBName   string
	User     string
	Password string
}

// Validate reports whether all five fields are present and the port is in the
// valid TCP range. The framework calls this at startup before handling any
// request and rejects missing or malformed values.
func (c DatabaseConfig) Validate() error {
	if c.Host == "" {
		return fmt.Errorf("database connection field host is empty")
	}
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("database connection field port %d is outside 1..65535", c.Port)
	}
	if c.DBName == "" {
		return fmt.Errorf("database connection field dbname is empty")
	}
	if c.User == "" {
		return fmt.Errorf("database connection field user is empty")
	}
	if c.Password == "" {
		return fmt.Errorf("database connection field password is empty")
	}
	return nil
}
