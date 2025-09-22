// nostr/subscriber.go
package nostr

import (
	"context"
	"database/sql"
	"log"
	"strings"
	"time"
	"turanga/config"

	"github.com/nbd-wtf/go-nostr"
)

// SubscriptionManager управляет подпиской на события Nostr.
type SubscriptionManager struct {
	client *Client
	cfg    *config.Config
	db     *sql.DB
}

// BookResponseData структура для данных книги в ответе Nostr
type BookResponseData struct {
	ID           int      `json:"id"`
	Title        string   `json:"title"`
	Authors      []string `json:"authors"`
	Series       string   `json:"series,omitempty"`
	SeriesNumber string   `json:"series_number,omitempty"`
	FileType     string   `json:"file_type"`
	FileHash     string   `json:"file_hash"`
	FileSize     int64    `json:"file_size"`
	IPFSCID      string   `json:"ipfs_cid,omitempty"`
}

// NewSubscriptionManager создает новый экземпляр SubscriptionManager.
func NewSubscriptionManager(client *Client, cfg *config.Config, db *sql.DB) *SubscriptionManager {
	return &SubscriptionManager{
		client: client,
		cfg:    cfg,
		db:     db,
	}
}

// Start запускает подписку на события запросов книг (kind 8698).
// Возвращает канал для остановки подписки.
func (sm *SubscriptionManager) Start(ctx context.Context) (stop chan struct{}, err error) {
	cfg := config.GetConfig()
	// Проверяем, инициализирован ли клиент Nostr
	if sm.client == nil {
		if cfg.Debug {
			log.Println("Nostr клиент не инициализирован, подписка на запросы книг пропущена")
		}
		return nil, nil // Не ошибка, просто ничего не делаем
	}

	// Проверяем, включен ли клиент Nostr
	if !sm.client.IsEnabled() {
		if cfg.Debug {
			log.Println("Nostr клиент отключен, подписка на запросы книг пропущена")
		}
		return nil, nil // Не ошибка, просто ничего не делаем
	}

	if cfg.Debug {
		log.Println("Запуск подписки на запросы книг через nostr (kind: 8698)...")
	}

	// Получаем список релеев из конфигурации или используем значения по умолчанию
	relays := []string{"wss://relay.damus.io", "wss://relay.primal.net"} // Значения по умолчанию
	if sm.cfg != nil && sm.cfg.NostrRelays != "" {
		relays = strings.Split(sm.cfg.NostrRelays, ",")
		for i, r := range relays {
			relays[i] = strings.TrimSpace(r)
		}
	}

	if cfg.Debug {
		log.Printf("Подписка на релеях: %v", relays)
	}

	// Создаем фильтр для подписки
	sinceTime := time.Now().Add(-4 * time.Hour) // 4 часа назад
	sinceTimestamp := nostr.Timestamp(sinceTime.Unix())

	filter := nostr.Filter{
		Kinds: []int{8698, 8699}, // Подписываемся на оба kinds
		Since: &sinceTimestamp,
	}

	// Создаем канал для сигнала остановки
	stop = make(chan struct{})

	// Подписываемся на каждый релей в отдельной горутине
	for _, relayURL := range relays {
		go func(url string) {
			// Параметры повторных попыток
			retryDelay := 1 * time.Second
			maxRetryDelay := 30 * time.Second

			// Основной цикл подключения с повторными попытками
			for {
				// Проверяем, не завершен ли контекст
				select {
				case <-ctx.Done():
					if cfg.Debug {
						log.Printf("Контекст завершен, останавливаем попытки подключения к релею %s", url)
					}
					return
				case <-stop:
					if cfg.Debug {
						log.Printf("Получен сигнал остановки, прекращаем попытки подключения к релею %s", url)
					}
					return
				default:
				}

				// Подключаемся к релею с таймаутом
				connectCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
				relay, err := nostr.RelayConnect(connectCtx, url)
				cancel()

				if err != nil {
					if cfg.Debug {
						log.Printf("Не удалось подключиться к релею %s для подписки: %v", url, err)
					}

					// Ждем перед повторной попыткой или проверяем сигналы завершения
					select {
					case <-ctx.Done():
						if cfg.Debug {
							log.Printf("Контекст завершен во время ожидания повторной попытки подключения к %s", url)
						}
						return
					case <-stop:
						if cfg.Debug {
							log.Printf("Получен сигнал остановки во время ожидания повторной попытки подключения к %s", url)
						}
						return
					case <-time.After(retryDelay):
						// Увеличиваем задержку экспоненциально
						retryDelay *= 2
						if retryDelay > maxRetryDelay {
							retryDelay = maxRetryDelay
						}
						continue // Повторяем попытку подключения
					}
				}

				// Сбросить задержку при успешном подключении
				retryDelay = 1 * time.Second

				if cfg.Debug {
					log.Printf("Подключились к релею %s для подписки", url)
				}

				// Подписываемся на события
				sub, err := relay.Subscribe(ctx, []nostr.Filter{filter})
				if err != nil {
					if cfg.Debug {
						log.Printf("Ошибка подписки на релей %s: %v", url, err)
					}
					relay.Close()
					continue // Повторяем попытку подключения
				}

				if sub == nil {
					if cfg.Debug {
						log.Printf("Подписка вернула nil для релея %s", url)
					}
					relay.Close()
					continue // Повторяем попытку подключения
				}

				log.Printf("Подписка (kind 8698/8699) на %s", url)

				// Цикл получения событий
			mainLoop:
				for {
					select {
					case event, ok := <-sub.Events:
						if !ok {
							// Канал закрыт, переподключаемся
							if cfg.Debug {
								log.Printf("Канал событий закрыт для релея %s, переподключаемся", url)
							}
							sub.Unsub()
							relay.Close()
							break mainLoop // Выходим из цикла событий и переподключаемся
						}
						// Получено новое событие
						if event != nil {
							if cfg.Debug {
								log.Printf("kind %d на %s от %s", event.Kind, url, event.PubKey)
							}
							// Обрабатываем событие
							sm.handleIncomingEvent(event)
						}
					case <-stop:
						// Получен сигнал остановки
						if cfg.Debug {
							log.Printf("Получен сигнал остановки подписки на релей %s", url)
						}
						sub.Unsub()
						relay.Close()
						break mainLoop
					case <-ctx.Done():
						// Контекст завершен (например, приложение останавливается)
						if cfg.Debug {
							log.Printf("Контекст завершен, останавливаем подписку на релей %s", url)
						}
						sub.Unsub()
						relay.Close()
						break mainLoop
					}
				}
				if cfg.Debug {
					log.Printf("Подписка на релей %s временно завершена, переподключаемся", url)
				}
			}
			log.Printf("Подписка на релей %s окончательно завершена", url) // такого не бывает
		}(relayURL)
	}

	if cfg.Debug {
		log.Println("Подписка на запросы книг через Nostr запущена")
	}
	return stop, nil
}

// getRequestCountForUser возвращает количество запросов от пользователя за указанный период
func (sm *SubscriptionManager) getRequestCountForUser(pubkey string, duration time.Duration) (int, error) {
	sinceTime := time.Now().Add(-duration).Unix()

	var count int
	err := sm.db.QueryRow(`
        SELECT COUNT(*) 
        FROM nostr_book_requests 
        WHERE pubkey = ? AND created_at >= ?
    `, pubkey, sinceTime).Scan(&count)

	if err != nil {
		return 0, err
	}

	return count, nil
}

// IncrementFriendDownloadCount увеличивает счетчик скачанных книг от друга
func (sm *SubscriptionManager) IncrementFriendDownloadCount(pubkey string) error {
	cfg := config.GetConfig()
	if pubkey == "" {
		return nil
	}

	now := time.Now().Unix()
	_, err := sm.db.Exec(`
		INSERT INTO friends (pubkey, download_count, last_download_at, created_at, updated_at)
		VALUES (?, 1, ?, ?, ?)
		ON CONFLICT(pubkey) DO UPDATE SET 
			download_count = download_count + 1,
			last_download_at = ?,
			updated_at = ?
	`, pubkey, now, now, now, now, now)

	if err != nil {
		if cfg.Debug {
			log.Printf("Ошибка увеличения счетчика для друга %s: %v", pubkey, err)
		}
		return err
	}

	if cfg.Debug {
		log.Printf("Увеличен счетчик скачанных книг для друга %s", pubkey)
	}
	return nil
}
