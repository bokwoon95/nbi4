package nbi4

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const configHelp = `Usage:
  notebrew config [KEY]                           # print the value of the key
  notebrew config [KEY] [VALUE]                   # set the value of the key
  notebrew config port                            # prints the value of port
  notebrew config port 443                        # sets the value of port to 443
  notebrew config database                        # prints the database configuration
  notebrew config database '{"dialect":"sqlite"}' # sets the database configuration
  notebrew config database.dialect sqlite         # sets the database dialect to sqlite

Keys:
  notebrew config port          # (txt) Port that notebrew listens on.
  notebrew config cmsdomain     # (txt) Domain that the CMS is served on.
  notebrew config contentdomain # (txt) Domain that the content is served on.
  notebrew config cdndomain     # (txt) Domain of the Content Delivery Network (CDN), if any.
  notebrew config lossyimgcmd   # (txt) Lossy image preprocessing command.
  notebrew config videocmd      # (txt) Video preprocessing command.
  notebrew config maxminddb     # (txt) Location of the MaxMind GeoLite2/GeoIP2 mmdb file.
  notebrew config database      # (json) Database configuration.
  notebrew config files         # (json) File system configuration.
  notebrew config objects       # (json) Object storage configuration.
  notebrew config captcha       # (json) Captcha configuration.
  notebrew config smtp          # (json) SMTP configuration.
  notebrew config proxy         # (json) Proxy configuration.
  notebrew config dns           # (json) DNS provider configuration.
  notebrew config tls           # (json) TLS configuration for TLS certificates.
  notebrew config monitoring    # (json) Error logging configuration.
To view notebrew's current settings, run ` + "`notebrew status`" + `.
`

type ConfigCmd struct {
	ConfigDir string
	Stdout    io.Writer
	Stderr    io.Writer
	Key       sql.NullString
	Value     sql.NullString
}

func ConfigCommand(configDir string, args ...string) (*ConfigCmd, error) {
	var cmd ConfigCmd
	cmd.ConfigDir = configDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	flagset := flag.NewFlagSet("", flag.ContinueOnError)
	flagset.Usage = func() {
		io.WriteString(flagset.Output(), configHelp)
	}
	err := flagset.Parse(args)
	if err != nil {
		return nil, err
	}
	args = flagset.Args()
	switch len(args) {
	case 0:
		break
	case 1:
		cmd.Key = sql.NullString{String: args[0], Valid: true}
	case 2:
		cmd.Key = sql.NullString{String: args[0], Valid: true}
		if strings.HasPrefix(args[1], "-") {
			return &cmd, nil
		}
		cmd.Value = sql.NullString{String: args[1], Valid: true}
	default:
		return nil, fmt.Errorf("too many arguments (max 2)")
	}
	if cmd.Value.String == "nil" {
		cmd.Value.String = ""
	}
	return &cmd, nil
}

func (cmd *ConfigCmd) Run() error {
	if !cmd.Key.Valid {
		io.WriteString(cmd.Stderr, configHelp)
		return nil
	}
	head, tail, _ := strings.Cut(cmd.Key.String, ".")
	switch head {
	case "":
		return fmt.Errorf("key cannot be empty")
	case "port":
		const filePath = "port.txt"
		if cmd.Value.Valid {
			err := os.WriteFile(filepath.Join(cmd.ConfigDir, filePath), []byte(cmd.Value.String), 0644)
			if err != nil {
				return err
			}
		} else {
			b, err := os.ReadFile(filepath.Join(cmd.ConfigDir, filePath))
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
			io.WriteString(cmd.Stdout, string(bytes.TrimSpace(b))+"\n")
		}
	case "domain":
		const filePath = "domain.txt"
		if cmd.Value.Valid {
			err := os.WriteFile(filepath.Join(cmd.ConfigDir, filePath), []byte(cmd.Value.String), 0644)
			if err != nil {
				return err
			}
		} else {
			b, err := os.ReadFile(filepath.Join(cmd.ConfigDir, filePath))
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
			io.WriteString(cmd.Stdout, string(bytes.TrimSpace(b))+"\n")
		}
	case "database":
		const filePath = "database.json"
		const help = databaseHelp
		var config DatabaseConfig
		b, err := os.ReadFile(filepath.Join(cmd.ConfigDir, filePath))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if len(b) > 0 {
			decoder := json.NewDecoder(bytes.NewReader(b))
			err = decoder.Decode(&config)
			if err != nil && tail != "" {
				return fmt.Errorf("%s: %w", filepath.Join(cmd.ConfigDir, filePath), err)
			}
		}
		if config.Params == nil {
			config.Params = map[string]string{}
		}
		switch tail {
		case "":
			if cmd.Value.Valid {
				var newConfig DatabaseConfig
				if cmd.Value.String != "" {
					decoder := json.NewDecoder(strings.NewReader(cmd.Value.String))
					err := decoder.Decode(&newConfig)
					if err != nil {
						return fmt.Errorf("invalid value: %w", err)
					}
				}
				config = newConfig
			} else {
				io.WriteString(cmd.Stderr, help)
				encoder := json.NewEncoder(cmd.Stdout)
				encoder.SetIndent("", "  ")
				encoder.SetEscapeHTML(false)
				err := encoder.Encode(config)
				if err != nil {
					return err
				}
			}
		case "dialect":
			if cmd.Value.Valid {
				config.Dialect = cmd.Value.String
			} else {
				io.WriteString(cmd.Stdout, config.Dialect+"\n")
			}
		case "sqliteFilePath":
			if cmd.Value.Valid {
				config.SQLiteFilePath = cmd.Value.String
			} else {
				io.WriteString(cmd.Stdout, config.SQLiteFilePath+"\n")
			}
		case "user":
			if cmd.Value.Valid {
				config.User = cmd.Value.String
			} else {
				io.WriteString(cmd.Stdout, config.User+"\n")
			}
		case "password":
			if cmd.Value.Valid {
				config.Password = cmd.Value.String
			} else {
				io.WriteString(cmd.Stdout, config.Password+"\n")
			}
		case "host":
			if cmd.Value.Valid {
				config.Host = cmd.Value.String
			} else {
				io.WriteString(cmd.Stdout, config.Host+"\n")
			}
		case "port":
			if cmd.Value.Valid {
				config.Port = cmd.Value.String
			} else {
				io.WriteString(cmd.Stdout, config.Port+"\n")
			}
		case "dbName":
			if cmd.Value.Valid {
				config.Port = cmd.Value.String
			} else {
				io.WriteString(cmd.Stdout, config.DBName+"\n")
			}
			config.DBName = cmd.Value.String
		case "params":
			if cmd.Value.Valid {
				var dict map[string]string
				if cmd.Value.String != "" {
					decoder := json.NewDecoder(strings.NewReader(cmd.Value.String))
					err := decoder.Decode(&dict)
					if err != nil {
						return fmt.Errorf("invalid value: %w", err)
					}
				}
				config.Params = dict
			} else {
				encoder := json.NewEncoder(cmd.Stdout)
				encoder.SetIndent("", "  ")
				encoder.SetEscapeHTML(false)
				err := encoder.Encode(config.Params)
				if err != nil {
					return err
				}
			}
		case "maxOpenConns":
			if cmd.Value.Valid {
				n, err := strconv.Atoi(cmd.Value.String)
				if err != nil {
					return fmt.Errorf("invalid value: %w", err)
				}
				config.MaxOpenConns = n
			} else {
				io.WriteString(cmd.Stdout, strconv.Itoa(config.MaxOpenConns)+"\n")
			}
		case "maxIdleConns":
			if cmd.Value.Valid {
				n, err := strconv.Atoi(cmd.Value.String)
				if err != nil {
					return fmt.Errorf("invalid value: %w", err)
				}
				config.MaxIdleConns = n
			} else {
				io.WriteString(cmd.Stdout, strconv.Itoa(config.MaxIdleConns)+"\n")
			}
		case "connMaxLifetime":
			if cmd.Value.Valid {
				config.ConnMaxLifetime = cmd.Value.String
			} else {
				io.WriteString(cmd.Stdout, config.ConnMaxLifetime+"\n")
			}
		case "connMaxIdleTime":
			if cmd.Value.Valid {
				config.ConnMaxIdleTime = cmd.Value.String
			} else {
				io.WriteString(cmd.Stdout, config.ConnMaxIdleTime+"\n")
			}
		default:
			io.WriteString(cmd.Stderr, help)
			return fmt.Errorf("%s: invalid key %q", cmd.Key.String, tail)
		}
		if cmd.Value.Valid {
			file, err := os.OpenFile(filepath.Join(cmd.ConfigDir, filePath), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
			if err != nil {
				return err
			}
			defer file.Close()
			encoder := json.NewEncoder(file)
			encoder.SetIndent("", "  ")
			encoder.SetEscapeHTML(false)
			err = encoder.Encode(config)
			if err != nil {
				return err
			}
			err = file.Close()
			if err != nil {
				return err
			}
		}
	case "objectstorage":
		const filePath = "objectstorage.json"
		const help = objectstorageHelp
		var config ObjectstorageConfig
		b, err := os.ReadFile(filepath.Join(cmd.ConfigDir, filePath))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if len(b) > 0 {
			decoder := json.NewDecoder(bytes.NewReader(b))
			err = decoder.Decode(&config)
			if err != nil && tail != "" {
				return fmt.Errorf("%s: %w", filepath.Join(cmd.ConfigDir, filePath), err)
			}
		}
		switch tail {
		case "":
			if cmd.Value.Valid {
				var newConfig ObjectstorageConfig
				if cmd.Value.String != "" {
					decoder := json.NewDecoder(strings.NewReader(cmd.Value.String))
					err := decoder.Decode(&newConfig)
					if err != nil {
						return err
					}
				}
				config = newConfig
			} else {
				io.WriteString(cmd.Stderr, help)
				encoder := json.NewEncoder(cmd.Stdout)
				encoder.SetIndent("", "  ")
				encoder.SetEscapeHTML(false)
				err := encoder.Encode(config)
				if err != nil {
					return err
				}
			}
		case "provider":
			if cmd.Value.Valid {
				config.Provider = cmd.Value.String
			} else {
				io.WriteString(cmd.Stdout, config.Provider+"\n")
			}
		case "directoryPath":
			if cmd.Value.Valid {
				config.DirectoryPath = cmd.Value.String
			} else {
				io.WriteString(cmd.Stdout, config.DirectoryPath+"\n")
			}
		case "endpoint":
			if cmd.Value.Valid {
				config.Endpoint = cmd.Value.String
			} else {
				io.WriteString(cmd.Stdout, config.Endpoint+"\n")
			}
		case "region":
			if cmd.Value.Valid {
				config.Region = cmd.Value.String
			} else {
				io.WriteString(cmd.Stdout, config.Region+"\n")
			}
		case "bucket":
			if cmd.Value.Valid {
				config.Bucket = cmd.Value.String
			} else {
				io.WriteString(cmd.Stdout, config.Bucket+"\n")
			}
		case "accessKeyID":
			if cmd.Value.Valid {
				config.AccessKeyID = cmd.Value.String
			} else {
				io.WriteString(cmd.Stdout, config.AccessKeyID+"\n")
			}
		case "secretAccessKey":
			if cmd.Value.Valid {
				config.SecretAccessKey = cmd.Value.String
			} else {
				io.WriteString(cmd.Stdout, config.SecretAccessKey+"\n")
			}
		default:
			io.WriteString(cmd.Stderr, help)
			return fmt.Errorf("%s: invalid key %q", cmd.Key.String, tail)
		}
		if cmd.Value.Valid {
			file, err := os.OpenFile(filepath.Join(cmd.ConfigDir, filePath), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
			if err != nil {
				return err
			}
			defer file.Close()
			encoder := json.NewEncoder(file)
			encoder.SetIndent("", "  ")
			encoder.SetEscapeHTML(false)
			err = encoder.Encode(config)
			if err != nil {
				return err
			}
			err = file.Close()
			if err != nil {
				return err
			}
		}
	case "captcha":
		const filePath = "captcha.json"
		const help = captchaHelp
		var config CaptchaConfig
		b, err := os.ReadFile(filepath.Join(cmd.ConfigDir, filePath))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if len(b) > 0 {
			decoder := json.NewDecoder(bytes.NewReader(b))
			err = decoder.Decode(&config)
			if err != nil {
				return fmt.Errorf("%s: %w", filepath.Join(cmd.ConfigDir, filePath), err)
			}
		}
		if config.CSP == nil {
			config.CSP = make(map[string]string)
		}
		switch tail {
		case "":
			if cmd.Value.Valid {
				var newConfig CaptchaConfig
				if cmd.Value.String != "" {
					decoder := json.NewDecoder(strings.NewReader(cmd.Value.String))
					err := decoder.Decode(&newConfig)
					if err != nil {
						return err
					}
				}
				config = newConfig
			} else {
				io.WriteString(cmd.Stderr, help)
				encoder := json.NewEncoder(cmd.Stdout)
				encoder.SetIndent("", "  ")
				encoder.SetEscapeHTML(false)
				err := encoder.Encode(config)
				if err != nil {
					return err
				}
			}
		case "widgetScriptSrc":
			if cmd.Value.Valid {
				config.WidgetScriptSrc = cmd.Value.String
			} else {
				io.WriteString(cmd.Stdout, config.WidgetScriptSrc+"\n")
			}
		case "widgetClass":
			if cmd.Value.Valid {
				config.WidgetClass = cmd.Value.String
			} else {
				io.WriteString(cmd.Stdout, config.WidgetClass+"\n")
			}
		case "verificationURL":
			if cmd.Value.Valid {
				config.VerificationURL = cmd.Value.String
			} else {
				io.WriteString(cmd.Stdout, config.VerificationURL+"\n")
			}
		case "responseTokenName":
			if cmd.Value.Valid {
				config.ResponseTokenName = cmd.Value.String
			} else {
				io.WriteString(cmd.Stdout, config.ResponseTokenName+"\n")
			}
		case "siteKey":
			if cmd.Value.Valid {
				config.SiteKey = cmd.Value.String
			} else {
				io.WriteString(cmd.Stdout, config.SiteKey+"\n")
			}
		case "secretKey":
			if cmd.Value.Valid {
				config.SecretKey = cmd.Value.String
			} else {
				io.WriteString(cmd.Stdout, config.SecretKey+"\n")
			}
		case "csp":
			if cmd.Value.Valid {
				var dict map[string]string
				if cmd.Value.String != "" {
					decoder := json.NewDecoder(strings.NewReader(cmd.Value.String))
					err := decoder.Decode(&dict)
					if err != nil {
						return err
					}
				}
				config.CSP = dict
			} else {
				encoder := json.NewEncoder(cmd.Stdout)
				encoder.SetIndent("", "  ")
				encoder.SetEscapeHTML(false)
				err := encoder.Encode(config.CSP)
				if err != nil {
					return err
				}
			}
		default:
			io.WriteString(cmd.Stderr, help)
			return fmt.Errorf("%s: invalid key %q", cmd.Key.String, tail)
		}
		if cmd.Value.Valid {
			file, err := os.OpenFile(filepath.Join(cmd.ConfigDir, filePath), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
			if err != nil {
				return err
			}
			defer file.Close()
			encoder := json.NewEncoder(file)
			encoder.SetIndent("", "  ")
			encoder.SetEscapeHTML(false)
			err = encoder.Encode(config)
			if err != nil {
				return err
			}
			err = file.Close()
			if err != nil {
				return err
			}
		}
	case "smtp":
		const filePath = "smtp.json"
		const help = smtpHelp
		var config SMTPConfig
		b, err := os.ReadFile(filepath.Join(cmd.ConfigDir, filePath))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if len(b) > 0 {
			decoder := json.NewDecoder(bytes.NewReader(b))
			err = decoder.Decode(&config)
			if err != nil && tail != "" {
				return fmt.Errorf("%s: %w", filepath.Join(cmd.ConfigDir, filePath), err)
			}
		}
		switch tail {
		case "":
			if cmd.Value.Valid {
				var newConfig SMTPConfig
				if cmd.Value.String != "" {
					decoder := json.NewDecoder(strings.NewReader(cmd.Value.String))
					err := decoder.Decode(&newConfig)
					if err != nil {
						return err
					}
				}
				config = newConfig
			} else {
				io.WriteString(cmd.Stderr, help)
				encoder := json.NewEncoder(cmd.Stdout)
				encoder.SetIndent("", "  ")
				err := encoder.Encode(config)
				if err != nil {
					return err
				}
			}
		case "username":
			if cmd.Value.Valid {
				config.Username = cmd.Value.String
			} else {
				io.WriteString(cmd.Stdout, config.Username+"\n")
			}
		case "password":
			if cmd.Value.Valid {
				config.Password = cmd.Value.String
			} else {
				io.WriteString(cmd.Stdout, config.Password+"\n")
			}
		case "host":
			if cmd.Value.Valid {
				config.Host = cmd.Value.String
			} else {
				io.WriteString(cmd.Stdout, config.Host+"\n")
			}
		case "port":
			if cmd.Value.Valid {
				config.Port = cmd.Value.String
			} else {
				io.WriteString(cmd.Stdout, config.Port+"\n")
			}
		case "mailFrom":
			if cmd.Value.Valid {
				config.MailFrom = cmd.Value.String
			} else {
				io.WriteString(cmd.Stdout, config.MailFrom+"\n")
			}
		case "replyTo":
			if cmd.Value.Valid {
				config.ReplyTo = cmd.Value.String
			} else {
				io.WriteString(cmd.Stdout, config.ReplyTo+"\n")
			}
		case "limitInterval":
			if cmd.Value.Valid {
				config.LimitInterval = cmd.Value.String
			} else {
				io.WriteString(cmd.Stdout, config.LimitInterval+"\n")
			}
		case "limitBurst":
			if cmd.Value.Valid {
				n, err := strconv.Atoi(cmd.Value.String)
				if err != nil {
					return fmt.Errorf("%s: %q is not an integer", cmd.Key.String, cmd.Value.String)
				}
				config.LimitBurst = n
			} else {
				io.WriteString(cmd.Stdout, strconv.Itoa(config.LimitBurst)+"\n")
			}
		default:
			io.WriteString(cmd.Stderr, help)
			return fmt.Errorf("%s: invalid key %q", cmd.Key.String, tail)
		}
		if cmd.Value.Valid {
			file, err := os.OpenFile(filepath.Join(cmd.ConfigDir, filePath), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
			if err != nil {
				return err
			}
			defer file.Close()
			encoder := json.NewEncoder(file)
			encoder.SetIndent("", "  ")
			err = encoder.Encode(config)
			if err != nil {
				return err
			}
			err = file.Close()
			if err != nil {
				return err
			}
		}
	case "proxy":
		const filePath = "proxy.json"
		const help = proxyHelp
		var config ProxyConfig
		b, err := os.ReadFile(filepath.Join(cmd.ConfigDir, filePath))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if len(b) > 0 {
			decoder := json.NewDecoder(bytes.NewReader(b))
			err = decoder.Decode(&config)
			if err != nil && tail != "" {
				return fmt.Errorf("%s: %w", filepath.Join(cmd.ConfigDir, filePath), err)
			}
		}
		if config.RealIPHeaders == nil {
			config.RealIPHeaders = map[string]string{}
		}
		if config.ProxyIPs == nil {
			config.ProxyIPs = []string{}
		}
		switch tail {
		case "":
			if cmd.Value.Valid {
				var newConfig ProxyConfig
				if cmd.Value.String != "" {
					decoder := json.NewDecoder(strings.NewReader(cmd.Value.String))
					err := decoder.Decode(&newConfig)
					if err != nil {
						return err
					}
				}
				config = newConfig
			} else {
				io.WriteString(cmd.Stderr, help)
				encoder := json.NewEncoder(cmd.Stdout)
				encoder.SetIndent("", "  ")
				encoder.SetEscapeHTML(false)
				err := encoder.Encode(config)
				if err != nil {
					return err
				}
			}
		case "realIPHeaders":
			if cmd.Value.Valid {
				var dict map[string]string
				if cmd.Value.String != "" {
					decoder := json.NewDecoder(strings.NewReader(cmd.Value.String))
					err := decoder.Decode(&dict)
					if err != nil {
						return err
					}
				}
				config.RealIPHeaders = dict
			} else {
				encoder := json.NewEncoder(cmd.Stdout)
				encoder.SetIndent("", "  ")
				encoder.SetEscapeHTML(false)
				err := encoder.Encode(config.RealIPHeaders)
				if err != nil {
					return err
				}
			}
		case "proxies":
			if cmd.Value.Valid {
				var list []string
				if cmd.Value.String != "" {
					decoder := json.NewDecoder(strings.NewReader(cmd.Value.String))
					err := decoder.Decode(&list)
					if err != nil {
						return err
					}
				}
				config.ProxyIPs = list
			} else {
				encoder := json.NewEncoder(cmd.Stdout)
				encoder.SetIndent("", "  ")
				encoder.SetEscapeHTML(false)
				err := encoder.Encode(config.ProxyIPs)
				if err != nil {
					return err
				}
			}
		default:
			io.WriteString(cmd.Stderr, help)
			return fmt.Errorf("%s: invalid key %q", cmd.Key.String, tail)
		}
		if cmd.Value.Valid {
			file, err := os.OpenFile(filepath.Join(cmd.ConfigDir, filePath), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
			if err != nil {
				return err
			}
			defer file.Close()
			encoder := json.NewEncoder(file)
			encoder.SetIndent("", "  ")
			encoder.SetEscapeHTML(false)
			err = encoder.Encode(config)
			if err != nil {
				return err
			}
			err = file.Close()
			if err != nil {
				return err
			}
		}
	case "dns":
		const filePath = "dns.json"
		const help = dnsHelp
		var config DNSConfig
		b, err := os.ReadFile(filepath.Join(cmd.ConfigDir, filePath))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if len(b) > 0 {
			decoder := json.NewDecoder(bytes.NewReader(b))
			err = decoder.Decode(&config)
			if err != nil && tail != "" {
				return fmt.Errorf("%s: %w", filepath.Join(cmd.ConfigDir, filePath), err)
			}
		}
		switch tail {
		case "":
			if cmd.Value.Valid {
				var newConfig DNSConfig
				if cmd.Value.String != "" {
					decoder := json.NewDecoder(strings.NewReader(cmd.Value.String))
					err := decoder.Decode(&newConfig)
					if err != nil {
						return err
					}
				}
				config = newConfig
			} else {
				io.WriteString(cmd.Stderr, help)
				encoder := json.NewEncoder(cmd.Stdout)
				encoder.SetIndent("", "  ")
				encoder.SetEscapeHTML(false)
				err := encoder.Encode(config)
				if err != nil {
					return err
				}
			}
		case "provider":
			if cmd.Value.Valid {
				config.Provider = cmd.Value.String
			} else {
				io.WriteString(cmd.Stdout, config.Provider+"\n")
			}
		case "username":
			if cmd.Value.Valid {
				config.Username = cmd.Value.String
			} else {
				io.WriteString(cmd.Stdout, config.Username+"\n")
			}
		case "apiKey":
			if cmd.Value.Valid {
				config.APIKey = cmd.Value.String
			} else {
				io.WriteString(cmd.Stdout, config.APIKey+"\n")
			}
		case "apiToken":
			if cmd.Value.Valid {
				config.APIToken = cmd.Value.String
			} else {
				io.WriteString(cmd.Stdout, config.APIToken+"\n")
			}
		case "secretKey":
			if cmd.Value.Valid {
				config.SecretKey = cmd.Value.String
			} else {
				io.WriteString(cmd.Stdout, config.SecretKey+"\n")
			}
		default:
			io.WriteString(cmd.Stderr, help)
			return fmt.Errorf("%s: invalid key %q", cmd.Key.String, tail)
		}
		if cmd.Value.Valid {
			file, err := os.OpenFile(filepath.Join(cmd.ConfigDir, filePath), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
			if err != nil {
				return err
			}
			defer file.Close()
			encoder := json.NewEncoder(file)
			encoder.SetIndent("", "  ")
			encoder.SetEscapeHTML(false)
			err = encoder.Encode(config)
			if err != nil {
				return err
			}
			err = file.Close()
			if err != nil {
				return err
			}
		}
	case "tls":
		const filePath = "tls.json"
		const help = tlsHelp
		var config TLSConfig
		b, err := os.ReadFile(filepath.Join(cmd.ConfigDir, filePath))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if len(b) > 0 {
			decoder := json.NewDecoder(bytes.NewReader(b))
			err = decoder.Decode(&config)
			if err != nil && tail != "" {
				return fmt.Errorf("%s: %w", filepath.Join(cmd.ConfigDir, filePath), err)
			}
		}
		switch tail {
		case "":
			if cmd.Value.Valid {
				var newConfig TLSConfig
				if cmd.Value.String != "" {
					decoder := json.NewDecoder(strings.NewReader(cmd.Value.String))
					err := decoder.Decode(&newConfig)
					if err != nil {
						return err
					}
				}
				config = newConfig
			} else {
				io.WriteString(cmd.Stderr, help)
				encoder := json.NewEncoder(cmd.Stdout)
				encoder.SetIndent("", "  ")
				encoder.SetEscapeHTML(false)
				err := encoder.Encode(config)
				if err != nil {
					return err
				}
			}
		case "provider":
			if cmd.Value.Valid {
				config.Provider = cmd.Value.String
			} else {
				io.WriteString(cmd.Stdout, config.Provider+"\n")
			}
		case "directoryPath":
			if cmd.Value.Valid {
				config.DirectoryPath = cmd.Value.String
			} else {
				io.WriteString(cmd.Stdout, config.DirectoryPath+"\n")
			}
		case "terseLogger":
			if cmd.Value.Valid {
				b, err := strconv.ParseBool(cmd.Value.String)
				if err != nil {
					return fmt.Errorf("invalid value: %w", err)
				}
				config.TerseLogger = b
			} else {
				io.WriteString(cmd.Stdout, strconv.FormatBool(config.TerseLogger)+"\n")
			}
		default:
			io.WriteString(cmd.Stderr, help)
			return fmt.Errorf("%s: invalid key %q", cmd.Key.String, tail)
		}
		if cmd.Value.Valid {
			file, err := os.OpenFile(filepath.Join(cmd.ConfigDir, filePath), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
			if err != nil {
				return err
			}
			defer file.Close()
			encoder := json.NewEncoder(file)
			encoder.SetIndent("", "  ")
			encoder.SetEscapeHTML(false)
			err = encoder.Encode(config)
			if err != nil {
				return err
			}
			err = file.Close()
			if err != nil {
				return err
			}
		}
	case "monitoring":
		const filePath = "monitoring.json"
		const help = monitoringHelp
		var config MonitoringConfig
		b, err := os.ReadFile(filepath.Join(cmd.ConfigDir, filePath))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if len(b) > 0 {
			decoder := json.NewDecoder(bytes.NewReader(b))
			err = decoder.Decode(&config)
			if err != nil && tail != "" {
				return fmt.Errorf("%s: %w", filepath.Join(cmd.ConfigDir, filePath), err)
			}
		}
		switch tail {
		case "":
			if cmd.Value.Valid {
				var newConfig MonitoringConfig
				if cmd.Value.String != "" {
					decoder := json.NewDecoder(strings.NewReader(cmd.Value.String))
					err := decoder.Decode(&newConfig)
					if err != nil {
						return err
					}
				}
				config = newConfig
			} else {
				io.WriteString(cmd.Stderr, help)
				encoder := json.NewEncoder(cmd.Stdout)
				encoder.SetIndent("", "  ")
				encoder.SetEscapeHTML(false)
				err := encoder.Encode(config)
				if err != nil {
					return err
				}
			}
		case "email":
			if cmd.Value.Valid {
				config.Email = cmd.Value.String
			} else {
				io.WriteString(cmd.Stdout, config.Email+"\n")
			}
		default:
			io.WriteString(cmd.Stderr, help)
			return fmt.Errorf("%s: invalid key %q", cmd.Key.String, tail)
		}
		if cmd.Value.Valid {
			file, err := os.OpenFile(filepath.Join(cmd.ConfigDir, filePath), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
			if err != nil {
				return err
			}
			defer file.Close()
			encoder := json.NewEncoder(file)
			encoder.SetIndent("", "  ")
			encoder.SetEscapeHTML(false)
			err = encoder.Encode(config)
			if err != nil {
				return err
			}
			err = file.Close()
			if err != nil {
				return err
			}
		}
	default:
		io.WriteString(cmd.Stderr, configHelp)
		return fmt.Errorf("%s: invalid key %q", cmd.Key.String, head)
	}
	return nil
}

const tlsHelp = `# == tls keys == #
# Refer to ` + "`notebrew config`" + ` on how to get and set config values.
# provider      - TLS certificate storage provider (possible values: database, directory).
# directoryPath - Directory to store TLS certificates in (if provider is directory).
# terseLogger   - If true, omit INFO logs when obtaining TLS certs (recommended to keep this false when starting out so you can debug any issues).
`

type TLSConfig struct {
	Provider      string `json:"provider"`
	DirectoryPath string `json:"directoryPath"`
	TerseLogger   bool   `json:"terseLogger"`
}

const databaseHelp = `# == database keys == #
# Refer to ` + "`notebrew config`" + ` on how to get and set config values.
# dialect         - Database dialect (possible values: sqlite, postgres, mysql).
# sqliteFilePath  - SQLite file path (if dialect is sqlite).
# user            - Database user.
# password        - Database password.
# host            - Database host.
# port            - Database port.
# dbName          - Database name.
# params          - Database-specific connection parameters (see https://example.com for more info).
# maxOpenConns    - Max open connections to the database (0 means unset, default is unlimited).
# maxIdleConns    - Max idle connections to the database (0 means unset, default is 2).
# connMaxLifetime - Connection max lifetime. e.g. 5m, 10m30s
# connMaxIdleTime - Connection max idle time. e.g. 5m, 10m30s
`

type DatabaseConfig struct {
	Dialect         string            `json:"dialect"`
	SQLiteFilePath  string            `json:"sqliteFilePath"`
	User            string            `json:"user"`
	Password        string            `json:"password"`
	Host            string            `json:"host"`
	Port            string            `json:"port"`
	DBName          string            `json:"dbName"`
	Params          map[string]string `json:"params"`
	MaxOpenConns    int               `json:"maxOpenConns"`
	MaxIdleConns    int               `json:"maxIdleConns"`
	ConnMaxLifetime string            `json:"connMaxLifetime"`
	ConnMaxIdleTime string            `json:"connMaxIdleTime"`
}

const objectstorageHelp = `# == objects keys == #
# Choose between using a directory or an S3-compatible provider to store objects.
# Refer to ` + "`notebrew config`" + ` on how to get and set config values.
# provider        - Object storage provider (possible values: directory, s3).
# directoryPath   - Object storage directory path (if using a directory).
# endpoint        - Object storage provider endpoint (if using s3). e.g. https://s3.us-east-1.amazonaws.com, https://s3.us-west-004.backblazeb2.com
# region          - S3 region. e.g. us-east-1, us-west-004
# bucket          - S3 bucket.
# accessKeyID     - S3 access key ID.
# secretAccessKey - S3 secret access key.
`

type ObjectstorageConfig struct {
	Provider        string `json:"provider"`
	DirectoryPath   string `json:"directoryPath"`
	Endpoint        string `json:"endpoint"`
	Region          string `json:"region"`
	Bucket          string `json:"bucket"`
	AccessKeyID     string `json:"accessKeyID"`
	SecretAccessKey string `json:"secretAccessKey"`
}

const captchaHelp = `# == captcha keys == #
# Refer to ` + "`notebrew config`" + ` on how to get and set config values.
# widgetScriptSrc   - Captcha widget's script src. e.g. https://js.hcaptcha.com/1/api.js, https://challenges.cloudflare.com/turnstile/v0/api.js
# widgetClass       - Captcha widget's container div class. e.g. h-captcha, cf-turnstile
# verificationURL   - Captcha verification URL to make POST requests to. e.g. https://api.hcaptcha.com/siteverify, https://challenges.cloudflare.com/turnstile/v0/siteverify
# responseTokenName - Captcha response token name. e.g. h-captcha-response, cf-turnstile-response
# siteKey           - Captcha site key.
# secretKey         - Captcha secret key.
# csp               - String-to-string mapping of Content-Security-Policy directive names to values for the captcha widget to work. e.g. {"script-src":"https://hcaptcha.com https://*.hcaptcha.com https://challenges.cloudflare.com","frame-src":"https://hcaptcha.com https://*.hcaptcha.com https://challenges.cloudflare.com","style-src":"https://hcaptcha.com https://*.hcaptcha.com","connect-src":"https://hcaptcha.com https://*.hcaptcha.com"}
`

type CaptchaConfig struct {
	WidgetScriptSrc   string            `json:"widgetScriptSrc"`
	WidgetClass       string            `json:"widgetClass"`
	VerificationURL   string            `json:"verificationURL"`
	ResponseTokenName string            `json:"responseTokenName"`
	SiteKey           string            `json:"siteKey"`
	SecretKey         string            `json:"secretKey"`
	CSP               map[string]string `json:"csp"`
}

const smtpHelp = `# == smtp keys == #
# username      - SMTP username.
# password      - SMTP password.
# host          - SMTP host.
# port          - SMTP port.
# mailFrom      - SMTP MAIL FROM address.
# replyTo       - SMTP Reply-To address.
# limitInterval - Interval for replenishing one token back to the rate limiter bucket. e.g 3m -> 480 emails per day, 5m -> 8760 emails per month, 1s -> 1 email per second (default is 3m)
# limitBurst    - Maximum tokens that can be held by the rate limiter bucket at any time. (default is 20)
`

type SMTPConfig struct {
	Username      string `json:"username"`
	Password      string `json:"password"`
	Host          string `json:"host"`
	Port          string `json:"port"`
	MailFrom      string `json:"mailFrom"`
	ReplyTo       string `json:"replyTo"`
	LimitInterval string `json:"limitInterval"`
	LimitBurst    int    `json:"limitBurst"`
}

const proxyHelp = `# == proxy keys == #
# Refer to ` + "`notebrew config`" + ` on how to get and set config values.
# realIPHeaders - String-to-string mapping of IP addresses to HTTP Headers which contain the real client IP.
# proxyIPs      - Array of proxy IP addresses.
`

type ProxyConfig struct {
	RealIPHeaders map[string]string `json:"realIPHeaders"`
	ProxyIPs      []string          `json:"proxyIPs"`
}

const dnsHelp = `# == dns keys == #
# Refer to ` + "`notebrew config`" + ` on how to get and set config values.
# provider  - DNS provider (possible values: namecheap, cloudflare, porkbun, godaddy).
# username  - DNS API username   (required by: namecheap).
# apiKey    - DNS API key        (required by: namecheap, porkbun).
# apiToken  - DNS API token      (required by: cloudflare, godaddy).
# secretKey - DNS API secret key (required by: porkbun).
`

type DNSConfig struct {
	Provider  string `json:"provider"`
	Username  string `json:"username"`
	APIKey    string `json:"apiKey"`
	APIToken  string `json:"apiToken"`
	SecretKey string `json:"secretKey"`
}

const monitoringHelp = `# == monitoring keys == #
# Refer to ` + "`notebrew config`" + ` on how to get and set config values.
# email - Email address to notify for errors. ` + "`notebrew config smtp`" + ` must be set.
`

type MonitoringConfig struct {
	Email string `json:"email"`
}
