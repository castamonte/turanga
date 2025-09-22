// nostr/nostr.go
package nostr

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"
	"turanga/config"

	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip19"
)

// Client обертка для работы с Nostr.
type Client struct {
	privateKey string
	publicKey  string
	enabled    bool // Будет true, если клиент может публиковать (есть ключ)
	cfg        *config.Config
	blacklist  *Blacklist
	db         *sql.DB
}

// BookRequestEventContent структура для содержимого события запроса книги
type BookRequestEventContent struct {
	Author   string `json:"author,omitempty"`
	Series   string `json:"series,omitempty"`
	Title    string `json:"title,omitempty"`
	FileHash string `json:"file_hash,omitempty"`
	Source   string `json:"source"`
}

// Mетоды для доступа к чёрному списку:
func (c *Client) GetBlacklist() *Blacklist {
	if c == nil {
		return nil
	}
	return c.blacklist
}

// GetPublicKey возвращает публичный ключ клиента в формате hex.
func (c *Client) GetPublicKey() string {
	if c == nil {
		return ""
	}
	return c.publicKey
}

// GetPublicKeyNpub возвращает публичный ключ клиента в формате npub.
func (c *Client) GetPublicKeyNpub() string {
	var cfg = config.GetConfig()
	if c == nil || c.publicKey == "" {
		return ""
	}
	npub, err := nip19.EncodePublicKey(c.publicKey)
	if err != nil {
		if cfg.Debug {
			log.Printf("Ошибка кодирования публичного ключа в npub: %v", err)
		}
		return c.publicKey // Возвращаем hex-версию в случае ошибки
	}
	return npub
}

// IsEnabled проверяет, активен ли клиент для публикации.
func (c *Client) IsEnabled() bool {
	return c != nil && c.enabled && c.privateKey != ""
}

// NewClient создает новый экземпляр Nostr клиента.
func NewClient(cfg *config.Config, db *sql.DB) (*Client, error) {
	if cfg.Debug {
		log.Println("Nostr клиент инициализируется...")
	}

	// Если ключ уже есть в конфиге
	if cfg.NostrPrivateKey != "" {
		// Парсим приватный ключ (nsec)
		prefix, sk, err := nip19.Decode(cfg.NostrPrivateKey)
		if err != nil {
			return nil, fmt.Errorf("ошибка декодирования nostr_private_key из конфига: %w", err)
		}

		// Проверяем, что префикс правильный для приватного ключа
		if prefix != "nsec" {
			return nil, fmt.Errorf("nostr_private_key в конфиге должен быть в формате nsec, got prefix %s", prefix)
		}

		// Приводим sk к строке (это и есть приватный ключ в hex формате)
		privKey, ok := sk.(string)
		if !ok {
			return nil, fmt.Errorf("неожиданный тип приватного ключа после декодирования: %T", sk)
		}

		// Получаем публичный ключ
		pubKey, err := nostr.GetPublicKey(privKey)
		if err != nil {
			return nil, fmt.Errorf("ошибка получения публичного ключа: %w", err)
		}

		// Кодируем публичный ключ в формат npub для логирования
		npub, err := nip19.EncodePublicKey(pubKey)
		if err != nil {
			if cfg.Debug {
				log.Printf("Предупреждение: Не удалось закодировать публичный ключ в npub: %v", err)
			}
			npub = pubKey // Используем hex-версию в случае ошибки
		}
		if cfg.Debug {
			log.Printf("Nostr клиент инициализирован. PublicKey (npub): %s", npub)
		}

		// Создаем и инициализируем чёрный список
		blacklist := NewBlacklist()

		// Загружаем чёрный список из файла
		blacklistFile := "blacklist.txt"
		if cfg.BlacklistFile != "" {
			blacklistFile = cfg.BlacklistFile
		}
		if err := blacklist.LoadFromFile(blacklistFile); err != nil {
			if cfg.Debug {
				log.Printf("Предупреждение: ошибка загрузки чёрного списка: %v", err)
			}
		}

		return &Client{
			privateKey: privKey,
			publicKey:  pubKey,
			enabled:    true,
			cfg:        cfg,
			blacklist:  blacklist,
			db:         db,
		}, nil
	}

	// Если ключа нет, генерируем новый
	log.Println("Приватный ключ Nostr не найден в конфигурации. Генерируем новый...")
	newPrivateKey := nostr.GeneratePrivateKey()

	newPublicKey, err := nostr.GetPublicKey(newPrivateKey)
	if err != nil {
		return nil, fmt.Errorf("ошибка получения публичного ключа для нового приватного: %w", err)
	}

	// Кодируем ключ в формат nsec для сохранения
	nsec, err := nip19.EncodePrivateKey(newPrivateKey)
	if err != nil {
		return nil, fmt.Errorf("ошибка кодирования нового приватного ключа в nsec: %w", err)
	}

	// Сохраняем новый ключ в конфиг
	cfg.NostrPrivateKey = nsec
	err = cfg.SaveConfig("turanga.conf")
	if err != nil {
		// Кодируем публичный ключ для логирования перед возвратом ошибки
		npub, npubErr := nip19.EncodePublicKey(newPublicKey)
		if npubErr != nil {
			if cfg.Debug {
				log.Printf("Предупреждение: Не удалось закодировать публичный ключ в npub: %v", npubErr)
			}
			npub = newPublicKey // Используем hex-версию
		}
		if cfg.Debug {
			log.Printf("Предупреждение: Не удалось автоматически сохранить новый Nostr приватный ключ в конфиг. Пожалуйста, добавьте его вручную: %s", nsec)
			log.Printf("Ваш новый публичный ключ (npub): %s", npub)
		}
		// Возвращаем клиент без сохранения ключа в конфиг, но с новым ключом в памяти
		// Это позволит использовать Nostr в текущей сессии, но ключ будет потерян при перезапуске,
		// если не будет сохранен вручную.

		// Создаем и инициализируем чёрный список
		blacklist := NewBlacklist()

		return &Client{
			privateKey: newPrivateKey,
			publicKey:  newPublicKey,
			enabled:    true, // Клиент включен, так как ключ есть в памяти
			blacklist:  blacklist,
			db:         db,
		}, nil
	}

	// Кодируем публичный ключ для логирования
	npub, err := nip19.EncodePublicKey(newPublicKey)
	if err != nil {
		if cfg.Debug {
			log.Printf("Предупреждение: Не удалось закодировать публичный ключ в npub: %v", err)
		}
		npub = newPublicKey // Используем hex-версию
	}

	log.Printf("Новый приватный ключ Nostr успешно сгенерирован и сохранен в конфигурации.")
	log.Printf("Ваш новый публичный ключ (npub): %s", npub)

	// Создаем и инициализируем чёрный список
	blacklist := NewBlacklist()

	// Загружаем чёрный список из файла
	blacklistFile := "blacklist.txt"
	if err := blacklist.LoadFromFile(blacklistFile); err != nil {
		if cfg.Debug {
			log.Printf("Предупреждение: ошибка загрузки чёрного списка: %v", err)
		}
	}

	return &Client{
		privateKey: newPrivateKey,
		publicKey:  newPublicKey,
		enabled:    true,
		cfg:        cfg,
		blacklist:  blacklist,
		db:         db,
	}, nil
}

// PublishBookRequestEvent публикует событие запроса книги (kind 8698)
func (c *Client) PublishBookRequestEvent(ctx context.Context, author, series, title, fileHash string) error {
	cfg := config.GetConfig()
	if !c.IsEnabled() {
		if cfg.Debug {
			log.Println("Nostr клиент не включен, публикация запроса книги пропущена")
		}
		return nil // Не ошибка, просто не публикуем
	}

	// Валидация запроса перед публикацией
	// Проверяем, что хотя бы одно поле заполнено
	if author == "" && series == "" && title == "" && fileHash == "" {
		log.Println("Игнорируем публикацию запроса: все поля пустые")
		return nil // Не ошибка, просто не публикуем
	}

	// Проверяем минимальную длину для серии и названия (если заданы)
	if series != "" && len(series) < 4 {
		if cfg.Debug {
			log.Printf("Игнорируем публикацию запроса: серия '%s' короче 4 символов", series)
		}
		return nil // Не ошибка, просто не публикуем
	}

	if title != "" && len(title) < 4 {
		if cfg.Debug {
			log.Printf("Игнорируем публикацию запроса: название '%s' короче 4 символов", title)
		}
		return nil // Не ошибка, просто не публикуем
	}

	// Проверяем формат хеша (если задан)
	if fileHash != "" {
		// Хеш должен быть длиной 16 символов и содержать только [a-f0-9]
		if len(fileHash) != 16 {
			if cfg.Debug {
				log.Printf("Игнорируем публикацию запроса: неверная длина хеша %d", len(fileHash))
			}
			return nil // Не ошибка, просто не публикуем
		}

		// Проверяем, что хеш содержит только допустимые символы
		valid := true
		for _, char := range fileHash {
			if !((char >= 'a' && char <= 'f') || (char >= '0' && char <= '9')) {
				valid = false
				break
			}
		}

		if !valid {
			if cfg.Debug {
				log.Printf("Игнорируем публикацию запроса: неверные символы в хеше '%s'", fileHash)
			}
			return nil // Не ошибка, просто не публикуем
		}
	}

	// Подготавливаем содержимое события
	content := BookRequestEventContent{
		Author:   author,
		Series:   series,
		Title:    title,
		FileHash: fileHash,
		Source:   "turanga",
	}

	// Сериализуем содержимое в JSON
	contentBytes, err := json.Marshal(content)
	if err != nil {
		return fmt.Errorf("ошибка сериализации содержимого запроса книги в JSON: %w", err)
	}

	// Создаем теги
	tags := nostr.Tags{}
	// Добавляем тег "Request"
	tags = append(tags, nostr.Tag{"t", "Request"})

	// Создаем событие
	ev := nostr.Event{
		PubKey:    c.publicKey,
		CreatedAt: nostr.Timestamp(time.Now().Unix()),
		Kind:      8698, // кастомный kind для запросов
		Tags:      tags,
		Content:   string(contentBytes),
	}

	// Подписываем событие
	err = ev.Sign(c.privateKey)
	if err != nil {
		return fmt.Errorf("ошибка подписания события запроса книги: %w", err)
	}

	// Сохраняем запрос в БД перед публикацией
	if c.db != nil {
		_, err = c.db.Exec(`
            INSERT INTO nostr_book_requests (event_id, pubkey, author, series, title, file_hash, created_at, processed, sent)
            VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
        `, ev.ID, c.publicKey, author, series, title, fileHash, ev.CreatedAt, 1, 1) // processed=1, sent=1
		if err != nil {
			if cfg.Debug {
				log.Printf("Предупреждение: ошибка сохранения запроса в БД: %v", err)
			}
			// Не останавливаем публикацию из-за ошибки БД
		}
	}

	// TODO: Получить список релays из конфигурации (cfg.NostrRelays)
	// Пока используем хардкод, как в примере NewClient
	relays := []string{"wss://relay.damus.io", "wss://relay.primal.net"} // Заглушка
	if c.cfg != nil && c.cfg.NostrRelays != "" {
		relays = strings.Split(c.cfg.NostrRelays, ",")
		for i, r := range relays {
			relays[i] = strings.TrimSpace(r)
		}
	}

	// Публикуем событие во все релays
	successCount := 0
	for _, relayURL := range relays {
		// Подключаемся к релay
		relay, err := nostr.RelayConnect(ctx, relayURL)
		if err != nil {
			if cfg.Debug {
				log.Printf("Не удалось подключиться к релay %s для публикации запроса: %v", relayURL, err)
			}
			continue
		}

		// Публикуем
		err = relay.Publish(ctx, ev)
		relay.Close() // Всегда закрываем соединение

		if err != nil {
			if cfg.Debug {
				log.Printf("Ошибка публикации запроса в релay %s: %v", relayURL, err)
			}
			continue
		}

		// Если err == nil, считаем, что публикация прошла успешно
		if cfg.Debug {
			log.Printf("Запрос книги успешно опубликован в релay %s", relayURL)
		}
		successCount++
	}

	if successCount == 0 {
		return fmt.Errorf("не удалось опубликовать запрос ни в одном релay")
	}

	log.Printf("Запрос книги опубликован успешно в %d релee(-ях)", successCount)
	return nil
}

// Mетод для перезагрузки чёрного списка:
func (c *Client) ReloadBlacklist() error {
	cfg := config.GetConfig()
	if c.blacklist == nil {
		return nil
	}

	blacklistFile := "blacklist.txt"
	if c.cfg != nil && c.cfg.BlacklistFile != "" {
		blacklistFile = c.cfg.BlacklistFile
	}

	if cfg.Debug {
		log.Printf("Перезагрузка чёрного списка из %s", blacklistFile)
	}
	return c.blacklist.LoadFromFile(blacklistFile)
}

// GetBlacklistFile возвращает путь к файлу чёрного списка
func (c *Client) GetBlacklistFile() string {
	if c.cfg != nil && c.cfg.BlacklistFile != "" {
		return c.cfg.BlacklistFile
	}
	return "blacklist.txt"
}
