package nbi4

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bokwoon95/nbi4/godaddy"
	"github.com/bokwoon95/nbi4/namecheap"
	"github.com/bokwoon95/nbi4/sq"
	"github.com/bokwoon95/nbi4/stacktrace"
	"github.com/bokwoon95/sqddl/ddl"
	"github.com/caddyserver/certmagic"
	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgconn"
	"github.com/libdns/cloudflare"
	"github.com/libdns/libdns"
	"github.com/libdns/porkbun"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"golang.org/x/sync/errgroup"
)

type Notebrew struct {
	// DB is the DB associated with the notebrew instance.
	DB *sql.DB

	// Dialect is Dialect of the database. Only sqlite, postgres and mysql
	// databases are supported.
	Dialect string

	// ErrorCode translates a database error into an dialect-specific error
	// code. If the error is not a database error or if no underlying
	// implementation is provided, ErrorCode should return an empty string.
	ErrorCode func(error) string

	// ObjectStorage is used for storage of binary objects.
	ObjectStorage ObjectStorage

	// Domain is the domain that the notebrew is using.
	// Examples: localhost, notebrew.com
	Domain string

	// (Required) Port is port that notebrew is listening on.
	Port int

	// PublicIP4 is the public IPv4 address of the current machine, if notebrew
	// is currently serving either port 80 (HTTP) or 443 (HTTPS).
	PublicIP4 netip.Addr

	// PublicIP6 is the public IPv6 address of the current machine, if notebrew
	// is currently serving either port 80 (HTTP) or 443 (HTTPS).
	PublicIP6 netip.Addr

	// LocalIP4 is the local IPv4 address of the current machine.
	LocalIP4 netip.Addr

	// LocalIP6 is the local IPv6 address of the current machine.
	LocalIP6 netip.Addr

	// Domains is the list of domains that need to point at notebrew for it to
	// work. Does not include user-created domains.
	Domains []string

	// ManagingDomains is the list of domains that the current instance of
	// notebrew is managing TLS certificates for.
	ManagingDomains []string

	// DNS provider (required for using wildcard certificates with
	// LetsEncrypt).
	DNSProvider interface {
		libdns.RecordAppender
		libdns.RecordDeleter
		libdns.RecordGetter
		libdns.RecordSetter
	}

	// CertStorage is the magic (certmagic) that automatically provisions TLS
	// certificates for notebrew.
	CertStorage certmagic.Storage

	// CertLogger is the logger used for a certmagic.Config.
	CertLogger *zap.Logger

	// ContentSecurityPolicy is the Content-Security-Policy HTTP header set for
	// every HTML response served on the CMS domain.
	ContentSecurityPolicy string

	// Logger is used for reporting errors that cannot be handled and are
	// thrown away.
	Logger *slog.Logger

	ModuleNamespaces []string

	Modules map[string]Module

	// BackgroundContext is the background context of the notebrew instance.
	BackgroundContext context.Context

	// backgroundCancel cancels the background context.
	backgroundCancel func()

	// BackgroundWaitGroup tracks the number of background jobs spawned by the
	// notebrew instance. Each background job should take in the background
	// context, and should should initiate shutdown when the background context
	// is canceled.
	BackgroundWaitGroup sync.WaitGroup
}

type Module interface {
	ID() string
	PreferredNamespace() string
	Initialize(nbrew *Notebrew, namespace string) error
	ServeHTTPContextData(w http.ResponseWriter, r *http.Request, contextData ContextData)
}

type ContextData struct {
	// == Application-level data == //
	CDNDomain   string       `json:"cdnDomain"`
	DevMode     bool         `json:"-"`
	NotebrewCSS template.CSS `json:"-"`
	NotebrewJS  template.JS  `json:"-"`
	// == Request-level data == //
	URLPath       string          `json:"urlPath"`
	PathTail      string          `json:"-"`
	UserID        ID              `json:"userID"`
	Username      string          `json:"username"`
	DisableReason string          `json:"disableReason"`
	UserFlags     map[string]bool `json:"userFlags"`
	Logger        *slog.Logger    `json:"-"`
	Referer       string          `json:"-"`
}

// New returns a new instance of Notebrew. Each field within it still needs to
// be manually configured.
func New(configDir, dataDir string, modules ...Module) (*Notebrew, error) {
	const NAMESPACE = "notebrew"

	backgroundContext, backgroundCancel := context.WithCancel(context.Background())
	nbrew := &Notebrew{
		BackgroundContext: backgroundContext,
		backgroundCancel:  backgroundCancel,
		Logger: slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			AddSource: true,
		})),
	}

	// Domain.
	b, err := os.ReadFile(filepath.Join(configDir, "domain.txt"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%s: %w", filepath.Join(configDir, "domain.txt"), err)
	}
	nbrew.Domain = string(bytes.TrimSpace(b))
	if nbrew.Domain == "0.0.0.0" {
		// LocalIP4 and LocalIP6.
		var dialer net.Dialer
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		group, groupctx := errgroup.WithContext(ctx)
		group.Go(func() error {
			conn, err := dialer.DialContext(groupctx, "udp", "8.8.8.8:80" /* Google IPv4 DNS */)
			if err != nil {
				return fmt.Errorf("udp 8.8.8.8:80: %w", err)
			}
			defer conn.Close()
			udpAddr := conn.LocalAddr().(*net.UDPAddr)
			ip, _ := netip.AddrFromSlice(udpAddr.IP)
			if ip.Is4() {
				nbrew.LocalIP4 = ip
			}
			return nil
		})
		group.Go(func() error {
			conn, err := dialer.DialContext(groupctx, "udp6", "[2001:4860:4860::8888]:80" /* Google IPv6 DNS */)
			if err != nil {
				// Best-effort attempt to get an IPv6 address; we won't always
				// have an IPv6 address e.g. when computer is using a phone's
				// data hotspot.
				return nil
			}
			defer conn.Close()
			udpAddr := conn.LocalAddr().(*net.UDPAddr)
			ip, _ := netip.AddrFromSlice(udpAddr.IP)
			if ip.Is6() {
				nbrew.LocalIP6 = ip
			}
			return nil
		})
		err := group.Wait()
		if err != nil {
			return nil, err
		}
		if !nbrew.LocalIP4.IsValid() && !nbrew.LocalIP6.IsValid() {
			return nil, fmt.Errorf("unable to determine the local IP address of the current machine")
		}
	}

	// Port.
	b, err = os.ReadFile(filepath.Join(configDir, "port.txt"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%s: %w", filepath.Join(configDir, "port.txt"), err)
	}
	port := string(bytes.TrimSpace(b))

	// Fill in the port and CMS domain if missing.
	if port != "" {
		nbrew.Port, err = strconv.Atoi(port)
		if err != nil {
			return nil, fmt.Errorf("%s: %q is not a valid integer", filepath.Join(configDir, "port.txt"), port)
		}
		if nbrew.Port <= 0 {
			return nil, fmt.Errorf("%s: %d is not a valid port", filepath.Join(configDir, "port.txt"), nbrew.Port)
		}
		if nbrew.Domain == "" {
			switch nbrew.Port {
			case 443:
				return nil, fmt.Errorf("%s: cannot use port 443 without specifying the domain", filepath.Join(configDir, "port.txt"))
			case 80:
				break // Use IP address as domain when we find it later.
			default:
				nbrew.Domain = "localhost:" + port
			}
		}
	} else {
		if nbrew.Domain != "" {
			if nbrew.Domain == "0.0.0.0" {
				nbrew.Port = 6444
			} else {
				nbrew.Port = 443
			}
		} else {
			nbrew.Port = 6444
			nbrew.Domain = "localhost"
		}
	}

	if nbrew.Port == 443 || nbrew.Port == 80 {
		// PublicIP4 and PublicIP6.
		client := &http.Client{
			Timeout: 10 * time.Second,
		}
		group, groupctx := errgroup.WithContext(context.Background())
		group.Go(func() error {
			request, err := http.NewRequest("GET", "https://ipv4.icanhazip.com", nil)
			if err != nil {
				return fmt.Errorf("ipv4.icanhazip.com: %w", err)
			}
			response, err := client.Do(request.WithContext(groupctx))
			if err != nil {
				return fmt.Errorf("ipv4.icanhazip.com: %w", err)
			}
			defer response.Body.Close()
			var b strings.Builder
			_, err = io.Copy(&b, response.Body)
			if err != nil {
				return fmt.Errorf("ipv4.icanhazip.com: %w", err)
			}
			err = response.Body.Close()
			if err != nil {
				return err
			}
			s := strings.TrimSpace(b.String())
			if s == "" {
				return nil
			}
			ip, err := netip.ParseAddr(s)
			if err != nil {
				return fmt.Errorf("ipv4.icanhazip.com: did not get a valid IP address (%s)", s)
			}
			if ip.Is4() {
				nbrew.PublicIP4 = ip
			}
			return nil
		})
		group.Go(func() error {
			request, err := http.NewRequest("GET", "https://ipv6.icanhazip.com", nil)
			if err != nil {
				return fmt.Errorf("ipv6.icanhazip.com: %w", err)
			}
			response, err := client.Do(request.WithContext(groupctx))
			if err != nil {
				return fmt.Errorf("ipv6.icanhazip.com: %w", err)
			}
			defer response.Body.Close()
			var b strings.Builder
			_, err = io.Copy(&b, response.Body)
			if err != nil {
				return fmt.Errorf("ipv6.icanhazip.com: %w", err)
			}
			err = response.Body.Close()
			if err != nil {
				return err
			}
			s := strings.TrimSpace(b.String())
			if s == "" {
				return nil
			}
			ip, err := netip.ParseAddr(s)
			if err != nil {
				return fmt.Errorf("ipv6.icanhazip.com: did not get a valid IP address (%s)", s)
			}
			if ip.Is6() {
				nbrew.PublicIP6 = ip
			}
			return nil
		})
		err := group.Wait()
		if err != nil {
			return nil, err
		}
		if !nbrew.PublicIP4.IsValid() && !nbrew.PublicIP6.IsValid() {
			return nil, fmt.Errorf("unable to determine the inbound IP address of the current machine")
		}
		if nbrew.Domain == "" {
			if nbrew.PublicIP4.IsValid() {
				nbrew.Domain = nbrew.PublicIP4.String()
			} else {
				nbrew.Domain = "[" + nbrew.PublicIP6.String() + "]"
			}
		}
	}

	// DNS.
	b, err = os.ReadFile(filepath.Join(configDir, "dns.json"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%s: %w", filepath.Join(configDir, "dns.json"), err)
	}
	b = bytes.TrimSpace(b)
	var dnsConfig DNSConfig
	if len(b) > 0 {
		decoder := json.NewDecoder(bytes.NewReader(b))
		err := decoder.Decode(&dnsConfig)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Join(configDir, "dns.json"), err)
		}
	}
	switch dnsConfig.Provider {
	case "":
		break
	case "namecheap":
		if dnsConfig.Username == "" {
			return nil, fmt.Errorf("%s: namecheap: missing username field", filepath.Join(configDir, "dns.json"))
		}
		if dnsConfig.APIKey == "" {
			return nil, fmt.Errorf("%s: namecheap: missing apiKey field", filepath.Join(configDir, "dns.json"))
		}
		if !nbrew.PublicIP4.IsValid() && (nbrew.Port == 443 || nbrew.Port == 80) {
			return nil, fmt.Errorf("the current machine's IP address (%s) is not IPv4: an IPv4 address is needed to integrate with namecheap's API", nbrew.PublicIP6.String())
		}
		nbrew.DNSProvider = &namecheap.Provider{
			APIKey:      dnsConfig.APIKey,
			User:        dnsConfig.Username,
			APIEndpoint: "https://api.namecheap.com/xml.response",
			ClientIP:    nbrew.PublicIP4.String(),
		}
	case "cloudflare":
		if dnsConfig.APIToken == "" {
			return nil, fmt.Errorf("%s: cloudflare: missing apiToken field", filepath.Join(configDir, "dns.json"))
		}
		nbrew.DNSProvider = &cloudflare.Provider{
			APIToken: dnsConfig.APIToken,
		}
	case "porkbun":
		if dnsConfig.APIKey == "" {
			return nil, fmt.Errorf("%s: porkbun: missing apiKey field", filepath.Join(configDir, "dns.json"))
		}
		if dnsConfig.SecretKey == "" {
			return nil, fmt.Errorf("%s: porkbun: missing secretKey field", filepath.Join(configDir, "dns.json"))
		}
		nbrew.DNSProvider = &porkbun.Provider{
			APIKey:       dnsConfig.APIKey,
			APISecretKey: dnsConfig.SecretKey,
		}
	case "godaddy":
		if dnsConfig.APIToken == "" {
			return nil, fmt.Errorf("%s: godaddy: missing apiToken field", filepath.Join(configDir, "dns.json"))
		}
		nbrew.DNSProvider = &godaddy.Provider{
			APIToken: dnsConfig.APIToken,
		}
	default:
		return nil, fmt.Errorf("%s: unsupported provider %q (possible values: namecheap, cloudflare, porkbun, godaddy)", filepath.Join(configDir, "dns.json"), dnsConfig.Provider)
	}

	// If Domain is not an IP address, add it to the Domains list.
	_, err = netip.ParseAddr(strings.TrimSuffix(strings.TrimPrefix(nbrew.Domain, "["), "]"))
	if err != nil {
		nbrew.Domains = append(nbrew.Domains, nbrew.Domain, "www."+nbrew.Domain)
	}

	// Database.
	b, err = os.ReadFile(filepath.Join(configDir, "database.json"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%s: %w", filepath.Join(configDir, "database.json"), err)
	}
	b = bytes.TrimSpace(b)
	var databaseConfig DatabaseConfig
	if len(b) > 0 {
		decoder := json.NewDecoder(bytes.NewReader(b))
		err := decoder.Decode(&databaseConfig)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Join(configDir, "database.json"), err)
		}
	}
	var dataSourceName string
	switch databaseConfig.Dialect {
	case "", "sqlite":
		if databaseConfig.SQLiteFilePath == "" {
			databaseConfig.SQLiteFilePath = filepath.Join(dataDir, "notebrew-database.db")
		}
		databaseConfig.SQLiteFilePath, err = filepath.Abs(databaseConfig.SQLiteFilePath)
		if err != nil {
			return nil, fmt.Errorf("%s: sqlite: %w", filepath.Join(configDir, "database.json"), err)
		}
		dataSourceName = databaseConfig.SQLiteFilePath + "?" + sqliteQueryString(databaseConfig.Params)
		nbrew.Dialect = "sqlite"
		nbrew.DB, err = sql.Open(sqliteDriverName, dataSourceName)
		if err != nil {
			return nil, fmt.Errorf("%s: sqlite: open %s: %w", filepath.Join(configDir, "database.json"), dataSourceName, err)
		}
		nbrew.ErrorCode = sqliteErrorCode
	case "postgres":
		values := make(url.Values)
		for key, value := range databaseConfig.Params {
			switch key {
			case "sslmode":
				values.Set(key, value)
			}
		}
		if _, ok := databaseConfig.Params["sslmode"]; !ok {
			values.Set("sslmode", "disable")
		}
		if databaseConfig.Port == "" {
			databaseConfig.Port = "5432"
		}
		uri := url.URL{
			Scheme:   "postgres",
			User:     url.UserPassword(databaseConfig.User, databaseConfig.Password),
			Host:     databaseConfig.Host + ":" + databaseConfig.Port,
			Path:     databaseConfig.DBName,
			RawQuery: values.Encode(),
		}
		dataSourceName = uri.String()
		nbrew.Dialect = "postgres"
		nbrew.DB, err = sql.Open("pgx", dataSourceName)
		if err != nil {
			return nil, fmt.Errorf("%s: postgres: open %s: %w", filepath.Join(configDir, "database.json"), dataSourceName, err)
		}
		nbrew.ErrorCode = func(err error) string {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) {
				return pgErr.Code
			}
			return ""
		}
	case "mysql":
		values := make(url.Values)
		for key, value := range databaseConfig.Params {
			switch key {
			case "charset", "collation", "loc", "maxAllowedPacket",
				"readTimeout", "rejectReadOnly", "serverPubKey", "timeout",
				"tls", "writeTimeout", "connectionAttributes":
				values.Set(key, value)
			}
		}
		values.Set("multiStatements", "true")
		values.Set("parseTime", "true")
		if databaseConfig.Port == "" {
			databaseConfig.Port = "3306"
		}
		config, err := mysql.ParseDSN(fmt.Sprintf("tcp(%s:%s)/%s?%s", databaseConfig.Host, databaseConfig.Port, url.PathEscape(databaseConfig.DBName), values.Encode()))
		if err != nil {
			return nil, err
		}
		// Set user and passwd manually to accomodate special characters.
		// https://github.com/go-sql-driver/mysql/issues/1323
		config.User = databaseConfig.User
		config.Passwd = databaseConfig.Password
		driver, err := mysql.NewConnector(config)
		if err != nil {
			return nil, err
		}
		dataSourceName = config.FormatDSN()
		nbrew.Dialect = "mysql"
		nbrew.DB = sql.OpenDB(driver)
		nbrew.ErrorCode = func(err error) string {
			var mysqlErr *mysql.MySQLError
			if errors.As(err, &mysqlErr) {
				return strconv.FormatUint(uint64(mysqlErr.Number), 10)
			}
			return ""
		}
	default:
		return nil, fmt.Errorf("%s: unsupported dialect %q (possible values: sqlite, postgres, mysql)", filepath.Join(configDir, "database.json"), databaseConfig.Dialect)
	}
	err = nbrew.DB.Ping()
	if err != nil {
		return nil, fmt.Errorf("%s: %s: ping %s: %w", filepath.Join(configDir, "database.json"), nbrew.Dialect, dataSourceName, err)
	}
	if databaseConfig.MaxOpenConns > 0 {
		nbrew.DB.SetMaxOpenConns(databaseConfig.MaxOpenConns)
	}
	if databaseConfig.MaxIdleConns > 0 {
		nbrew.DB.SetMaxIdleConns(databaseConfig.MaxIdleConns)
	}
	if databaseConfig.ConnMaxLifetime != "" {
		duration, err := time.ParseDuration(databaseConfig.ConnMaxLifetime)
		if err != nil {
			return nil, fmt.Errorf("%s: connMaxLifetime: %s: %w", filepath.Join(configDir, "database.json"), databaseConfig.ConnMaxLifetime, err)
		}
		nbrew.DB.SetConnMaxLifetime(duration)
	}
	if databaseConfig.ConnMaxIdleTime != "" {
		duration, err := time.ParseDuration(databaseConfig.ConnMaxIdleTime)
		if err != nil {
			return nil, fmt.Errorf("%s: connMaxIdleTime: %s: %w", filepath.Join(configDir, "database.json"), databaseConfig.ConnMaxIdleTime, err)
		}
		nbrew.DB.SetConnMaxIdleTime(duration)
	}
	databaseCatalog := &ddl.Catalog{
		Dialect: nbrew.Dialect,
	}
	err = UnmarshalCatalog(databaseCatalog, schemaJSON, "notebrew")
	if err != nil {
		return nil, err
	}
	automigrateCmd := &ddl.AutomigrateCmd{
		DB:             nbrew.DB,
		Dialect:        nbrew.Dialect,
		DestCatalog:    databaseCatalog,
		AcceptWarnings: true,
		Stderr:         io.Discard,
	}
	err = automigrateCmd.Run()
	if err != nil {
		return nil, err
	}

	// Object Storage.
	b, err = os.ReadFile(filepath.Join(configDir, "objectstorage.json"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%s: %w", filepath.Join(configDir, "objectstorage.json"), err)
	}
	b = bytes.TrimSpace(b)
	var objectstorageConfig ObjectstorageConfig
	if len(b) > 0 {
		decoder := json.NewDecoder(bytes.NewReader(b))
		err = decoder.Decode(&objectstorageConfig)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Join(configDir, "objectstorage.json"), err)
		}
	}
	switch objectstorageConfig.Provider {
	case "", "directory":
		if objectstorageConfig.DirectoryPath == "" {
			objectstorageConfig.DirectoryPath = filepath.Join(dataDir, "notebrew-objectstorage")
		} else {
			objectstorageConfig.DirectoryPath = filepath.Clean(objectstorageConfig.DirectoryPath)
		}
		err := os.MkdirAll(objectstorageConfig.DirectoryPath, 0755)
		if err != nil {
			return nil, err
		}
		objectStorage, err := NewDirObjectStorage(objectstorageConfig.DirectoryPath, os.TempDir())
		if err != nil {
			return nil, err
		}
		nbrew.ObjectStorage = objectStorage
	case "s3":
		if objectstorageConfig.Endpoint == "" {
			return nil, fmt.Errorf("%s: missing endpoint field", filepath.Join(configDir, "objectstorage.json"))
		}
		if objectstorageConfig.Region == "" {
			return nil, fmt.Errorf("%s: missing region field", filepath.Join(configDir, "objectstorage.json"))
		}
		if objectstorageConfig.Bucket == "" {
			return nil, fmt.Errorf("%s: missing bucket field", filepath.Join(configDir, "objectstorage.json"))
		}
		if objectstorageConfig.AccessKeyID == "" {
			return nil, fmt.Errorf("%s: missing accessKeyID field", filepath.Join(configDir, "objectstorage.json"))
		}
		if objectstorageConfig.SecretAccessKey == "" {
			return nil, fmt.Errorf("%s: missing secretAccessKey field", filepath.Join(configDir, "objectstorage.json"))
		}
		contentTypeMap := map[string]string{
			".jpeg": "image/jpeg",
			".jpg":  "image/jpeg",
			".png":  "image/png",
			".webp": "image/webp",
			".gif":  "image/gif",
			".mp4":  "video/mp4",
			".mov":  "video/mp4",
			".webm": "video/webm",
			".tgz":  "application/octet-stream",
		}
		objectStorage, err := NewS3Storage(context.Background(), S3StorageConfig{
			Endpoint:        objectstorageConfig.Endpoint,
			Region:          objectstorageConfig.Region,
			Bucket:          objectstorageConfig.Bucket,
			AccessKeyID:     objectstorageConfig.AccessKeyID,
			SecretAccessKey: objectstorageConfig.SecretAccessKey,
			ContentTypeMap:  contentTypeMap,
			Logger:          nbrew.Logger,
		})
		if err != nil {
			return nil, err
		}
		nbrew.ObjectStorage = objectStorage
	default:
		return nil, fmt.Errorf("%s: unsupported provider %q (possible values: directory, s3)", filepath.Join(configDir, "objectstorage.json"), objectstorageConfig.Provider)
	}

	// TLS.
	b, err = os.ReadFile(filepath.Join(configDir, "tls.json"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%s: %w", filepath.Join(configDir, "tls.json"), err)
	}
	b = bytes.TrimSpace(b)
	var certmagicConfig TLSConfig
	if len(b) > 0 {
		decoder := json.NewDecoder(bytes.NewReader(b))
		err := decoder.Decode(&certmagicConfig)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Join(configDir, "tls.json"), err)
		}
	}
	switch certmagicConfig.Provider {
	case "database": // TODO: Once CertDatabaseStorage is implemented, make it the default instead.
		// nbrew.CertStorage = &CertDatabaseStorage{
		// 	DB:        nbrew.DB,
		// 	Dialect:   nbrew.Dialect,
		// 	ErrorCode: nbrew.ErrorCode,
		// }
	case "", "directory":
		if certmagicConfig.DirectoryPath == "" {
			certmagicConfig.DirectoryPath = filepath.Join(configDir, "certmagic")
		}
		err = os.MkdirAll(certmagicConfig.DirectoryPath, 0755)
		if err != nil {
			return nil, err
		}
		nbrew.CertStorage = &certmagic.FileStorage{
			Path: certmagicConfig.DirectoryPath,
		}
	}
	if certmagicConfig.TerseLogger {
		encoderConfig := zap.NewProductionEncoderConfig()
		encoderConfig.EncodeTime = zapcore.RFC3339TimeEncoder
		terseLogger := zap.New(zapcore.NewCore(
			zapcore.NewConsoleEncoder(encoderConfig),
			os.Stderr,
			zap.ErrorLevel,
		))
		nbrew.CertLogger = terseLogger
		certmagic.Default.Logger = terseLogger
		certmagic.DefaultACME.Logger = terseLogger
	} else {
		encoderConfig := zap.NewProductionEncoderConfig()
		encoderConfig.EncodeTime = zapcore.RFC3339TimeEncoder
		verboseLogger := zap.New(zapcore.NewCore(
			zapcore.NewConsoleEncoder(encoderConfig),
			os.Stderr,
			zap.InfoLevel,
		))
		nbrew.CertLogger = verboseLogger
		certmagic.Default.Logger = verboseLogger
		certmagic.DefaultACME.Logger = verboseLogger
	}

	if nbrew.Port == 443 || nbrew.Port == 80 {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		group, groupctx := errgroup.WithContext(ctx)
		matched := make([]bool, len(nbrew.Domains))
		for i, domain := range nbrew.Domains {
			group.Go(func() error {
				_, err := netip.ParseAddr(domain)
				if err == nil {
					return nil
				}
				ips, err := net.DefaultResolver.LookupIPAddr(groupctx, domain)
				if err != nil {
					fmt.Println(err)
					return nil
				}
				for _, ip := range ips {
					ip, ok := netip.AddrFromSlice(ip.IP)
					if !ok {
						continue
					}
					if ip.Is4() && ip == nbrew.PublicIP4 || ip.Is6() && ip == nbrew.PublicIP6 {
						matched[i] = true
						break
					}
				}
				return nil
			})
		}
		err = group.Wait()
		if err != nil {
			return nil, err
		}
		switch nbrew.Port {
		case 80:
			for i, domain := range nbrew.Domains {
				if matched[i] {
					nbrew.ManagingDomains = append(nbrew.ManagingDomains, domain)
				}
			}
		case 443:
			addedDomainWildcard := false
			for i, domain := range nbrew.Domains {
				if matched[i] {
					if certmagic.MatchWildcard(domain, "*."+nbrew.Domain) && nbrew.DNSProvider != nil {
						if !addedDomainWildcard {
							addedDomainWildcard = true
							nbrew.ManagingDomains = append(nbrew.ManagingDomains, "*."+nbrew.Domain)
						}
					} else {
						nbrew.ManagingDomains = append(nbrew.ManagingDomains, domain)
					}
				}
			}
		}
	}

	// Content Security Policy.
	var buf strings.Builder
	// // default-src
	// buf.WriteString("default-src 'none';")
	// // script-src
	// buf.WriteString(" script-src 'self' 'unsafe-hashes' " + notebrewJSHash)
	// if value := csp["script-src"]; value != "" {
	// 	buf.WriteString(" " + value)
	// }
	// if value := nbrew.CaptchaConfig.CSP["script-src"]; value != "" {
	// 	buf.WriteString(" " + value)
	// }
	// buf.WriteString(";")
	// // connect-src
	// buf.WriteString(" connect-src 'self'")
	// if value := csp["connect-src"]; value != "" {
	// 	buf.WriteString(" " + value)
	// }
	// if value := nbrew.CaptchaConfig.CSP["connect-src"]; value != "" {
	// 	buf.WriteString(" " + value)
	// }
	// buf.WriteString(";")
	// // img-src
	// buf.WriteString(" img-src 'self' data:")
	// if value := csp["img-src"]; value != "" {
	// 	buf.WriteString(" " + value)
	// }
	// if nbrew.CDNDomain != "" {
	// 	buf.WriteString(" " + nbrew.CDNDomain)
	// }
	// buf.WriteString(";")
	// // media-src
	// buf.WriteString(" media-src 'self'")
	// if value := csp["media-src"]; value != "" {
	// 	buf.WriteString(" " + value)
	// }
	// if nbrew.CDNDomain != "" {
	// 	buf.WriteString(" " + nbrew.CDNDomain)
	// }
	// buf.WriteString(";")
	// // style-src
	// buf.WriteString(" style-src 'self' 'unsafe-inline'")
	// if value := csp["style-src"]; value != "" {
	// 	buf.WriteString(" " + value)
	// }
	// if value := nbrew.CaptchaConfig.CSP["style-src"]; value != "" {
	// 	buf.WriteString(" " + value)
	// }
	// buf.WriteString(";")
	// // base-uri
	// buf.WriteString(" base-uri 'self';")
	// // form-action
	// buf.WriteString(" form-action 'self'")
	// if value := csp["form-action"]; value != "" {
	// 	buf.WriteString(" " + value)
	// }
	// buf.WriteString(";")
	// // manifest-src
	// buf.WriteString(" manifest-src 'self';")
	// // frame-src
	// buf.WriteString(" frame-src 'self'")
	// if value := csp["frame-src"]; value != "" {
	// 	buf.WriteString(" " + value)
	// }
	// if value := nbrew.CaptchaConfig.CSP["frame-src"]; value != "" {
	// 	buf.WriteString(" " + value)
	// }
	// buf.WriteString(";")
	// // font-src
	// buf.WriteString(" font-src 'self';")
	nbrew.ContentSecurityPolicy = buf.String()

	for _, module := range modules {
		id := module.ID()
		namespace, err := sq.FetchOne(context.Background(), nbrew.DB, sq.Query{
			Dialect: nbrew.Dialect,
			Format:  "SELECT {*} FROM " + NAMESPACE + "_module WHERE id = {id}",
			Values: []any{
				sq.StringParam("id", id),
			},
		}, func(row *sq.Row) string {
			return row.String("namespace")
		})
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				return nil, stacktrace.New(err)
			}
			inserted := false
			preferredNamespace := module.PreferredNamespace()
			for i := range 10 {
				namespace = preferredNamespace
				if i > 0 {
					namespace += strconv.Itoa(i)
				}
				_, err := sq.Exec(context.Background(), nbrew.DB, sq.Query{
					Dialect: nbrew.Dialect,
					Format:  "INSERT INTO " + NAMESPACE + "_module (id, namespace) VALUES ({id}, {namespace})",
					Values: []any{
						sq.StringParam("id", id),
						sq.StringParam("namespace", namespace),
					},
				})
				if err != nil {
					if nbrew.ErrorCode == nil || !IsKeyViolation(nbrew.Dialect, nbrew.ErrorCode(err)) {
						return nil, stacktrace.New(err)
					}
				} else {
					inserted = true
					break
				}
			}
			if !inserted {
				return nil, fmt.Errorf("unable to provision namespace for module %s (preferred namespace: %s)", id, preferredNamespace)
			}
		}
		err = module.Initialize(nbrew, namespace)
		if err != nil {
			return nil, fmt.Errorf("initializing module %s: %w", id, err)
		}
	}
	return nbrew, nil
}

// IsKeyViolation returns true if the provided errorCode matches the
// dialect-specific code for representing a primary key/unique constraint
// violation.
func IsKeyViolation(dialect string, errorCode string) bool {
	switch dialect {
	case "sqlite":
		return errorCode == "1555" || errorCode == "2067" // SQLITE_CONSTRAINT_PRIMARYKEY, SQLITE_CONSTRAINT_UNIQUE
	case "postgres":
		return errorCode == "23505" // unique_violation
	case "mysql":
		return errorCode == "1062" // ER_DUP_ENTRY
	case "sqlserver":
		return errorCode == "2627"
	default:
		return false
	}
}
