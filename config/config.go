// config/config.go
package config

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	shell "github.com/ipfs/go-ipfs-api"
	"gopkg.in/ini.v1"
)

const (
	// DefaultMaxAnnotationLen максимальная длина аннотации
	DefaultMaxAnnotationLen = 2025
)

var globalConfig *Config

// SetGlobalConfig устанавливает глобальную конфигурацию
func SetGlobalConfig(cfg *Config) {
	globalConfig = cfg
}

// GetConfig возвращает глобальную конфигурацию
func GetConfig() *Config {
	return globalConfig
}

// Config структура для хранения конфигурации приложения
type Config struct {
	Debug                  bool   `ini:"debug"`
	Port                   int    `ini:"port"`
	BooksDir               string `ini:"books_dir"`
	RenameBook             string `ini:"rename_book"` // "no", "autit", "hash"
	PasswordHash           string `ini:"password_hash"`
	CatalogTitle           string `ini:"catalog_title"`
	LocalIPFSAPI           string `ini:"local_ipfs_api"`
	LocalIPFSGateway       string `ini:"local_ipfs_gateway"`
	IPFSGateway            string `ini:"ipfs_gateway"`
	RemoveFromIPFSOnDelete bool   `ini:"remove_from_ipfs_on_delete"`
	PaginationThreshold    int    `ini:"pagination_threshold"`
	NostrPrivateKey        string `ini:"nostr_private_key"`
	NostrRelays            string `ini:"nostr_relays"`
	BlacklistFile          string `ini:"blacklist_file"`
	MaxRequestsPerDay      int    `ini:"max_requests_per_day"`
}

// DefaultConfig возвращает конфигурацию по умолчанию
func DefaultConfig() *Config {
	return &Config{
		Debug:                  false,
		Port:                   8698,
		BooksDir:               "./books",
		RenameBook:             "no",
		PasswordHash:           "",
		CatalogTitle:           "Turanga - Каталог книг",
		LocalIPFSAPI:           "127.0.0.1:5001",
		LocalIPFSGateway:       "http://127.0.0.1:8080",
		IPFSGateway:            "https://dweb.link",
		RemoveFromIPFSOnDelete: false,
		PaginationThreshold:    60,
		NostrPrivateKey:        "",
		NostrRelays:            "wss://relay.damus.io,wss://relay.primal.net",
		BlacklistFile:          "blacklist.txt",
		MaxRequestsPerDay:      10,
	}
}

// LoadConfig загружает конфигурацию из файла
func LoadConfig(configPath string) (*Config, error) {
	cfg := DefaultConfig()

	// Проверяем существование файла конфигурации
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		log.Printf("Файл конфигурации %s не найден, использую настройки по умолчанию", configPath)
		return cfg, nil
	}

	// Загружаем INI-файл
	iniCfg, err := ini.Load(configPath)
	if err != nil {
		return nil, fmt.Errorf("ошибка загрузки файла конфигурации %s: %w", configPath, err)
	}

	// Читаем секцию по умолчанию
	defaultSection := iniCfg.Section("")

	// Функция для безопасного чтения строковых значений
	readString := func(key string, defaultValue string) string {
		if value := defaultSection.Key(key).String(); value != "" {
			return value
		}
		return defaultValue
	}

	// Функция для безопасного чтения целочисленных значений
	readInt := func(key string, defaultValue int) int {
		if value, err := defaultSection.Key(key).Int(); err == nil {
			return value
		}
		return defaultValue
	}

	// Функция для безопасного чтения булевых значений
	readBool := func(key string, defaultValue bool) bool {
		if value, err := defaultSection.Key(key).Bool(); err == nil {
			return value
		}
		return defaultValue
	}

	// Заполняем структуру конфигурации значениями из файла
	cfg.Debug = readBool("debug", cfg.Debug)
	cfg.Port = readInt("port", cfg.Port)
	cfg.BooksDir = readString("books_dir", cfg.BooksDir)
	cfg.RenameBook = readString("rename_book", cfg.RenameBook)
	cfg.PasswordHash = readString("password_hash", cfg.PasswordHash)
	cfg.CatalogTitle = readString("catalog_title", cfg.CatalogTitle)
	cfg.LocalIPFSAPI = readString("ipfs_api_address", cfg.LocalIPFSAPI)
	cfg.LocalIPFSGateway = readString("ipfs_gateway_address", cfg.LocalIPFSGateway)
	cfg.IPFSGateway = readString("ipfs_gateway", cfg.IPFSGateway)
	cfg.RemoveFromIPFSOnDelete = readBool("remove_from_ipfs_on_delete", cfg.RemoveFromIPFSOnDelete)
	cfg.PaginationThreshold = readInt("pagination_threshold", cfg.PaginationThreshold)
	cfg.NostrPrivateKey = readString("nostr_private_key", cfg.NostrPrivateKey)
	cfg.NostrRelays = readString("nostr_relays", cfg.NostrRelays)
	cfg.BlacklistFile = readString("blacklist_file", cfg.BlacklistFile)
	cfg.MaxRequestsPerDay = readInt("max_requests_per_day", cfg.MaxRequestsPerDay)

	return cfg, nil
}

// Validate проверяет корректность конфигурации
func (c *Config) Validate() error {
	// Логирование состояния debug режима
	/*
		if c.Debug {
			log.Println("Включен режим отладки (debug=true)")
		} else {
			log.Println("Режим отладки выключен (debug=false)")
		}
	*/

	// Проверяем порт
	if c.Port <= 0 || c.Port > 65535 {
		return fmt.Errorf("некорректный порт: %d (должен быть от 1 до 65535)", c.Port)
	}

	// Проверяем BooksDir
	if c.BooksDir == "" {
		return fmt.Errorf("каталог с книгами (books_dir) не может быть пустым")
	}

	// Проверяем, что это допустимый путь (базовая проверка)
	if strings.ContainsAny(c.BooksDir, "\x00") {
		return fmt.Errorf("каталог с книгами (books_dir) содержит недопустимые символы")
	}

	// Валидация нового поля rename_book
	c.RenameBook = strings.ToLower(strings.TrimSpace(c.RenameBook))
	validRenameOptions := map[string]bool{"no": true, "autit": true, "hash": true}
	if !validRenameOptions[c.RenameBook] {
		// Устанавливаем значение по умолчанию, если указано недопустимое значение
		log.Printf("Недопустимое значение rename_book: '%s'. Использую 'no' по умолчанию.", c.RenameBook)
		c.RenameBook = "no"
	}

	// Проверяем CatalogTitle
	if c.CatalogTitle == "" {
		c.CatalogTitle = "Turanga - Каталог книг" // Устанавливаем значение по умолчанию
	}

	// Проверяем LocalIPFSAPI (может быть пустым, если IPFS не используется)
	// Но если задан, проверяем формат (простейшая проверка)
	if c.LocalIPFSAPI != "" {
		// Можно добавить более строгую проверку формата адреса
		// Например, regexp.MustCompile(`^[\w\.-]+:\d+$`).MatchString(c.IPFSAPIAddress)
		// Пока оставим базовую
		if strings.ContainsAny(c.LocalIPFSAPI, " \t\n\r") {
			return fmt.Errorf("IPFS API address содержит недопустимые символы пробела: %s", c.LocalIPFSAPI)
		}
	}

	// Проверяем LocalIPFSGateway (может быть пустым)
	if c.LocalIPFSGateway != "" {
		// Простейшая проверка, что это похоже на URL
		if !strings.HasPrefix(c.LocalIPFSGateway, "http://") && !strings.HasPrefix(c.LocalIPFSGateway, "https://") {
			// Можно установить значение по умолчанию или вернуть ошибку
			// Пока просто логируем предупреждение
			log.Printf("Предупреждение: IPFS Gateway Address не начинается с http:// или https://: %s", c.LocalIPFSGateway)
		}
		if strings.ContainsAny(c.LocalIPFSGateway, " \t\n\r") {
			return fmt.Errorf("IPFS Gateway Address содержит недопустимые символы пробела: %s", c.LocalIPFSGateway)
		}
	}

	// Проверяем IPFSGateway
	if c.IPFSGateway != "" {
		// Простейшая проверка, что это похоже на URL
		if !strings.HasPrefix(c.IPFSGateway, "http://") && !strings.HasPrefix(c.IPFSGateway, "https://") {
			log.Printf("Предупреждение: IPFS Gateway не начинается с http:// или https://: %s", c.IPFSGateway)
		}
		if strings.ContainsAny(c.IPFSGateway, " \t\n\r") {
			return fmt.Errorf("IPFS Gateway содержит недопустимые символы пробела: %s", c.IPFSGateway)
		}
	} else {
		// Если не задан, используем значение по умолчанию
		c.IPFSGateway = "https://dweb.link"
	}

	// Проверяем PaginationThreshold
	if c.PaginationThreshold <= 0 {
		log.Printf("Недопустимое значение pagination_threshold: %d. Использую 60 по умолчанию.", c.PaginationThreshold)
		c.PaginationThreshold = 60
	}

	// NostrPrivateKey валидируется при использовании, здесь просто проверим формат
	if c.NostrPrivateKey != "" && !strings.HasPrefix(c.NostrPrivateKey, "nsec") {
		// Можно добавить более строгую проверку формата nsec ключа
		log.Printf("Предупреждение: Nostr Private Key не начинается с 'nsec'. Проверьте формат.")
	}

	// Проверяем NostrRelays (может быть пустым, но если заданы, проверяем формат)
	if c.NostrRelays != "" {
		relays := strings.Split(c.NostrRelays, ",")
		for i, relay := range relays {
			relay = strings.TrimSpace(relay)
			if relay != "" && !strings.HasPrefix(relay, "wss://") && !strings.HasPrefix(relay, "ws://") {
				log.Printf("Предупреждение: Nostr Relay #%d не начинается с 'wss://' или 'ws://': %s", i+1, relay)
			}
			relays[i] = relay
		}
		// Нормализуем список релays
		c.NostrRelays = strings.Join(relays, ",")
	}

	// Проверяем BlacklistFile
	if c.BlacklistFile == "" {
		c.BlacklistFile = "blacklist.txt" // Устанавливаем значение по умолчанию
	}

	// Проверяем MaxRequestsPerDay
	if c.MaxRequestsPerDay <= 0 {
		c.MaxRequestsPerDay = 10 // Значение по умолчанию
	}

	return nil
}

// String возвращает строковое представление конфигурации
func (c *Config) String() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Debug: %t\n", c.Debug))
	sb.WriteString(fmt.Sprintf("Port: %d\n", c.Port))
	sb.WriteString(fmt.Sprintf("BooksDir: %s\n", c.BooksDir))
	sb.WriteString(fmt.Sprintf("RenameBook: %s\n", c.RenameBook))
	sb.WriteString(fmt.Sprintf("PasswordHash: %s\n", c.PasswordHash))
	sb.WriteString(fmt.Sprintf("CatalogTitle: %s\n", c.CatalogTitle))
	sb.WriteString(fmt.Sprintf("LocalIPFSAPI: %s\n", c.LocalIPFSAPI))
	sb.WriteString(fmt.Sprintf("LocalIPFSGateway: %s\n", c.LocalIPFSGateway))
	sb.WriteString(fmt.Sprintf("IPFSGateway: %s\n", c.IPFSGateway))
	sb.WriteString(fmt.Sprintf("RemoveFromIPFSOnDelete: %t\n", c.RemoveFromIPFSOnDelete))
	sb.WriteString(fmt.Sprintf("PaginationThreshold: %d\n", c.PaginationThreshold))
	sb.WriteString(fmt.Sprintf("NostrPrivateKey: %s\n", func() string {
		if c.NostrPrivateKey == "" {
			return "(не задан)"
		}
		// Не показываем полный ключ в логах, только префикс
		if len(c.NostrPrivateKey) > 8 {
			return c.NostrPrivateKey[:8] + "..."
		}
		return "(скрыт)"
	}()))
	sb.WriteString(fmt.Sprintf("NostrRelays: %s\n", c.NostrRelays))
	sb.WriteString(fmt.Sprintf("BlacklistFile: %s\n", c.BlacklistFile))
	sb.WriteString(fmt.Sprintf("MaxRequestsPerDay: %d\n", c.MaxRequestsPerDay))

	return sb.String()
}

// GetAbsolutePath возвращает абсолютный путь для относительного пути из конфига
// rootPath - путь к корню приложения
func (c *Config) GetAbsolutePath(rootPath, relativePath string) string {
	if filepath.IsAbs(relativePath) {
		return relativePath
	}
	return filepath.Join(rootPath, relativePath)
}

// GetBooksDirAbs возвращает абсолютный путь к каталогу с книгами
func (c *Config) GetBooksDirAbs(rootPath string) string {
	return c.GetAbsolutePath(rootPath, c.BooksDir)
}

// GetRenameBook возвращает значение настройки rename_book
func (c *Config) GetRenameBook() string {
	// Уже валидировано в Validate, но на всякий случай
	validRenameOptions := map[string]bool{"no": true, "autit": true, "hash": true}
	if validRenameOptions[c.RenameBook] {
		return c.RenameBook
	}
	return "no" // Значение по умолчанию, если что-то пошло не так
}

// GetCatalogTitle возвращает название каталога
func (c *Config) GetCatalogTitle() string {
	if c.CatalogTitle == "" {
		return "Turanga - Каталог книг"
	}
	return c.CatalogTitle
}

// GetMaxAnnotationLength возвращает максимальную длину аннотации
func (c *Config) GetMaxAnnotationLength() int {
	// Всегда возвращаем захардкоженное значение
	return DefaultMaxAnnotationLen
}

// SaveConfig сохраняет конфигурацию в файл
func (c *Config) SaveConfig(configPath string) error {
	// Преобразуем путь в абсолютный, если он относительный
	absConfigPath := configPath
	if !filepath.IsAbs(configPath) {
		// Получаем путь к исполняемому файлу
		execPath, err := os.Executable()
		if err != nil {
			return fmt.Errorf("ошибка получения пути к исполняемому файлу: %w", err)
		}
		execDir := filepath.Dir(execPath)
		absConfigPath = filepath.Join(execDir, configPath)
	}

	// Создаем новую INI конфигурацию
	cfg := ini.Empty()
	section := cfg.Section("")

	// Записываем все значения
	section.Key("port").SetValue(fmt.Sprintf("%d", c.Port))
	section.Key("books_dir").SetValue(c.BooksDir)
	section.Key("rename_book").SetValue(c.RenameBook)
	section.Key("catalog_title").SetValue(c.CatalogTitle)
	section.Key("ipfs_api_address").SetValue(c.LocalIPFSAPI)
	section.Key("ipfs_gateway_address").SetValue(c.LocalIPFSGateway)
	section.Key("ipfs_gateway").SetValue(c.IPFSGateway)
	section.Key("remove_from_ipfs_on_delete").SetValue(fmt.Sprintf("%t", c.RemoveFromIPFSOnDelete))
	section.Key("pagination_threshold").SetValue(fmt.Sprintf("%d", c.PaginationThreshold))
	section.Key("nostr_private_key").SetValue(c.NostrPrivateKey)
	section.Key("nostr_relays").SetValue(c.NostrRelays)
	section.Key("blacklist_file").SetValue(c.BlacklistFile)
	section.Key("max_requests_per_day").SetValue(fmt.Sprintf("%d", c.MaxRequestsPerDay))

	// Сохраняем хэш пароля, если он есть
	if c.PasswordHash != "" {
		section.Key("password_hash").SetValue(c.PasswordHash)
	}

	// Создаем каталог, если он не существует
	configDir := filepath.Dir(absConfigPath)
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("ошибка создания каталога для конфигурации: %w", err)
	}

	// Сохраняем в файл
	return cfg.SaveTo(absConfigPath)
}

// Метод для получения клиента IPFS shell
func (c *Config) GetIPFSShell() (*shell.Shell, error) {
	if c.LocalIPFSAPI == "" {
		return nil, fmt.Errorf("Local IPFS API address is not set")
	}
	return shell.NewShell(c.LocalIPFSAPI), nil
}

// GetIPFSGateway возвращает шлюз IPFS
func (c *Config) GetIPFSGateway() string {
	if c.IPFSGateway != "" {
		return c.IPFSGateway
	}
	return "https://dweb.link" // Значение по умолчанию
}

// GetIPFSLink возвращает полную ссылку на IPFS ресурс
func (c *Config) GetIPFSLink(cid string) string {
	if cid == "" {
		return ""
	}
	gateway := c.GetIPFSGateway()
	// Убираем слэш в конце gateway, если есть
	gateway = strings.TrimRight(gateway, "/")
	// Убираем слэш в начале cid, если есть
	cid = strings.TrimLeft(cid, "/")
	return fmt.Sprintf("%s/%s", gateway, cid)
}
