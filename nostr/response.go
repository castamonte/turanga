// nostr/response.go
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
)

// sendResponse отправляет ответ на запрос книги через Nostr.
func (sm *SubscriptionManager) sendResponse(requesterPubKey string, bookIDs []int64, requestEventID string) {
	cfg := config.GetConfig()
	if cfg.Debug {
		log.Printf("Начинаем формировать и отправлять ответ на запрос %s пользователю %s. Найдено книг: %d",
			requestEventID, requesterPubKey, len(bookIDs))
	}

	// Проверяем, инициализирован ли клиент Nostr для отправки ответа
	if sm.client == nil {
		if cfg.Debug {
			log.Println("Nostr клиент не инициализирован, отправка ответа пропущена")
		}
		return
	}
	if !sm.client.IsEnabled() {
		if cfg.Debug {
			log.Println("Nostr клиент отключен, отправка ответа пропущена")
		}
		return
	}

	// Проверяем, не был ли ответ уже отправлен
	var isSent bool
	err := sm.db.QueryRow("SELECT COALESCE(sent, 0) FROM nostr_book_requests WHERE event_id = ?", requestEventID).Scan(&isSent)
	if err != nil {
		if err == sql.ErrNoRows {
			if cfg.Debug {
				log.Printf("Запрос %s не найден в БД", requestEventID)
			}
			return
		}
		if cfg.Debug {
			log.Printf("Ошибка проверки статуса отправки для запроса %s: %v", requestEventID, err)
		}
		return
	}
	if isSent {
		if cfg.Debug {
			log.Printf("Ответ на запрос %s уже отправлен, повторная отправка не требуется", requestEventID)
		}
		return
	}

	// 1. Получаем данные найденных книг из БД
	var booksData []BookResponseData
	for _, bookID := range bookIDs {
		var book BookResponseData
		err := sm.db.QueryRow(`
			SELECT b.id, b.title, b.series, b.series_number, b.file_type, b.file_hash, b.file_size, b.ipfs_cid
			FROM books b
			WHERE b.id = ?
		`, bookID).Scan(&book.ID, &book.Title, &book.Series, &book.SeriesNumber, &book.FileType, &book.FileHash, &book.FileSize, &book.IPFSCID)

		if err != nil {
			if err == sql.ErrNoRows {
				if cfg.Debug {
					log.Printf("Книга ID %d не найдена", bookID)
				}
			} else {
				if cfg.Debug {
					log.Printf("Ошибка запроса данных книги ID %d: %v", bookID, err)
				}
			}
			continue
		}

		// 1.1. Получаем авторов книги
		authorsQuery := `
            SELECT CASE 
                WHEN COUNT(*) > 2 THEN 'коллектив авторов'
                WHEN COUNT(*) = 0 THEN 'Автор не указан'
                ELSE GROUP_CONCAT(a.full_name, ', ')
            END as authors_str
            FROM book_authors ba 
            LEFT JOIN authors a ON ba.author_id = a.id 
            WHERE ba.book_id = ?
        `
		var authorsStr sql.NullString
		err = sm.db.QueryRow(authorsQuery, bookID).Scan(&authorsStr)
		if err != nil && err != sql.ErrNoRows {
			if cfg.Debug {
				log.Printf("Ошибка запроса авторов книги ID %d: %v", bookID, err)
			}
			book.Authors = []string{"Автор не указан"}
		} else {
			if authorsStr.Valid && authorsStr.String != "" && authorsStr.String != "Автор не указан" && authorsStr.String != "коллектив авторов" {
				book.Authors = strings.Split(authorsStr.String, ", ")
			} else if authorsStr.Valid && authorsStr.String == "коллектив авторов" {
				book.Authors = []string{"коллектив авторов"}
			} else {
				book.Authors = []string{"автор не указан"}
			}
		}

		booksData = append(booksData, book)
	}

	if len(booksData) == 0 {
		if cfg.Debug {
			log.Printf("Нет подходящих книг для отправки в ответе на запрос %s", requestEventID)
		}
		return
	}

	if cfg.Debug {
		log.Printf("Подготовлено %d книг для отправки в ответе на запрос %s", len(booksData), requestEventID)
	}

	// 2. Формируем содержимое события ответа
	responseContent, err := json.Marshal(booksData)
	if err != nil {
		if cfg.Debug {
			log.Printf("Ошибка сериализации данных книг в JSON для ответа на запрос %s: %v", requestEventID, err)
		}
		return
	}

	// 3. Создаем теги для события ответа
	tags := nostr.Tags{}
	tags = append(tags, nostr.Tag{"e", requestEventID, "", "reply"})
	tags = append(tags, nostr.Tag{"p", requesterPubKey})
	tags = append(tags, nostr.Tag{"t", "Response"})
	tags = append(tags, nostr.Tag{"client", "turanga"})

	// 4. Создаем событие ответа
	responseEvent := nostr.Event{
		PubKey:    sm.client.GetPublicKey(),
		CreatedAt: nostr.Timestamp(time.Now().Unix()),
		Kind:      8699,
		Tags:      tags,
		Content:   string(responseContent),
	}

	// 5. Подписываем событие ответа
	err = responseEvent.Sign(sm.client.privateKey)
	if err != nil {
		if cfg.Debug {
			log.Printf("Ошибка подписания события ответа на запрос %s: %v", requestEventID, err)
		}
		return
	}

	// 6. Получаем список релеев для публикации ответа
	relays := []string{"wss://relay.damus.io", "wss://relay.primal.net"}
	if sm.cfg != nil && sm.cfg.NostrRelays != "" {
		relays = strings.Split(sm.cfg.NostrRelays, ",")
		for i, r := range relays {
			relays[i] = strings.TrimSpace(r)
		}
	}

	// 7. Публикуем событие ответа во все релеи
	successCount := 0
	for _, relayURL := range relays {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)

		relay, err := nostr.RelayConnect(ctx, relayURL)
		cancel()
		if err != nil {
			if cfg.Debug {
				log.Printf("Не удалось подключиться к релею %s для отправки ответа: %v", relayURL, err)
			}
			continue
		}

		pubCtx, pubCancel := context.WithTimeout(context.Background(), 10*time.Second)
		err = relay.Publish(pubCtx, responseEvent)
		pubCancel()
		relay.Close()

		if err != nil {
			if cfg.Debug {
				log.Printf("Ошибка публикации ответа в релей %s: %v", relayURL, err)
			}
			continue
		}

		if cfg.Debug {
			log.Printf("Ответ на запрос %s успешно опубликован в релей %s", requestEventID, relayURL)
		}
		successCount++
	}

	if successCount == 0 {
		if cfg.Debug {
			log.Printf("Не удалось опубликовать ответ на запрос %s ни в одном релей", requestEventID)
		}
		return
	}

	if cfg.Debug {
		log.Printf("Ответ на запрос %s успешно отправлен пользователю %s через %d релей(ев)",
			requestEventID, requesterPubKey, successCount)
	}

	// 8. Помечаем запрос как отправленный
	_, err = sm.db.Exec("UPDATE nostr_book_requests SET sent = TRUE WHERE event_id = ?", requestEventID)
	if err != nil {
		if cfg.Debug {
			log.Printf("Ошибка обновления статуса отправки для запроса %s: %v", requestEventID, err)
		}
	}
}

// removeBooksFromPendingResponse удаляет книги из подготовленных к ответу
// если они уже есть в полученных ответах от других нод
func (sm *SubscriptionManager) removeBooksFromPendingResponse(requestEventID string, receivedFileHashes map[string]bool) {
	cfg := config.GetConfig()
	if len(receivedFileHashes) == 0 || requestEventID == "" {
		return
	}

	// Получаем ID запроса по event_id
	var requestID int64
	err := sm.db.QueryRow("SELECT id FROM nostr_book_requests WHERE event_id = ?", requestEventID).Scan(&requestID)
	if err != nil {
		if err != sql.ErrNoRows {
			if cfg.Debug {
				log.Printf("Ошибка получения ID запроса по event_id %s: %v", requestEventID, err)
			}
		}
		return
	}

	// Формируем список file_hash для удаления
	fileHashList := make([]interface{}, 0, len(receivedFileHashes))
	placeholders := make([]string, 0, len(receivedFileHashes))

	for hash := range receivedFileHashes {
		if hash != "" {
			fileHashList = append(fileHashList, hash)
			placeholders = append(placeholders, "?")
		}
	}

	if len(fileHashList) == 0 {
		return
	}

	// Удаляем книги из подготовленных к ответу
	query := fmt.Sprintf(
		"DELETE FROM nostr_request_books WHERE request_id = ? AND file_hash IN (%s)",
		strings.Join(placeholders, ","),
	)

	args := append([]interface{}{requestID}, fileHashList...)

	result, err := sm.db.Exec(query, args...)
	if err != nil {
		if cfg.Debug {
			log.Printf("Ошибка удаления книг из подготовленных к ответу: %v", err)
		}
		return
	}

	affected, err := result.RowsAffected()
	if err != nil {
		if cfg.Debug {
			log.Printf("Ошибка получения количества удаленных записей: %v", err)
		}
	} else if affected > 0 {
		if cfg.Debug {
			log.Printf("Вычеркнуто %d книг из подготовленных к ответу на запрос %s", affected, requestEventID)
		}

		// Проверяем, остались ли еще книги для отправки
		var remainingCount int
		err = sm.db.QueryRow("SELECT COUNT(*) FROM nostr_request_books WHERE request_id = ?", requestID).Scan(&remainingCount)
		if err != nil {
			if cfg.Debug {
				log.Printf("Ошибка проверки оставшихся книг: %v", err)
			}
		} else if remainingCount == 0 {
			log.Printf("Не осталось книг для ответа на запрос %s. Отмена отправки ответа.", requestEventID)

			// Помечаем запрос как обработанный, но не отправленный, чтобы избежать повторной попытки
			_, err = sm.db.Exec("UPDATE nostr_book_requests SET processed = 1, sent = 0 WHERE event_id = ?", requestEventID)
			if err != nil {
				if cfg.Debug {
					log.Printf("Ошибка обновления статуса запроса %s при отмене отправки: %v", requestEventID, err)
				}
			} else {
				if cfg.Debug {
					log.Printf("Запрос %s помечен как обработанный без отправки ответа", requestEventID)
				}
			}
		}
	}
}

// CleanupOldResponses удаляет ответы, которые не относятся к последнему запросу
func (sm *SubscriptionManager) CleanupOldResponses() {
	// Получаем event_id последнего нашего запроса
	var lastRequestEventID string
	err := sm.db.QueryRow(`
        SELECT event_id 
        FROM nostr_book_requests 
        WHERE pubkey = ? AND sent = 1
        ORDER BY created_at DESC 
        LIMIT 1
    `, sm.client.GetPublicKey()).Scan(&lastRequestEventID)

	if err != nil {
		if err == sql.ErrNoRows {
			// Нет отправленных запросов - удаляем все ответы
			log.Printf("Нет отправленных запросов, удаляем все ответы")
			_, err = sm.db.Exec("DELETE FROM nostr_received_responses")
			if err != nil {
				log.Printf("Ошибка удаления всех ответов: %v", err)
			}
			return
		}
		log.Printf("Ошибка получения последнего запроса: %v", err)
		return
	}

	log.Printf("Очищаем ответы, не относящиеся к последнему запросу: %s", lastRequestEventID)

	// Удаляем ответы, которые не относятся к последнему запросу
	result, err := sm.db.Exec(`
        DELETE FROM nostr_received_responses 
        WHERE request_event_id != ?
        AND request_event_id IN (
            SELECT event_id FROM nostr_book_requests WHERE pubkey = ?
        )
    `, lastRequestEventID, sm.client.GetPublicKey())

	if err != nil {
		log.Printf("Ошибка очистки старых ответов: %v", err)
		return
	}

	deleted, _ := result.RowsAffected()
	log.Printf("Удалено %d старых ответов", deleted)

	// Также удаляем связанные книги из ответов
	if deleted > 0 {
		result, err := sm.db.Exec(`
            DELETE FROM nostr_response_books 
            WHERE response_id IN (
                SELECT id FROM nostr_received_responses 
                WHERE request_event_id != ?
                AND request_event_id IN (
                    SELECT event_id FROM nostr_book_requests WHERE pubkey = ?
                )
            )
        `, lastRequestEventID, sm.client.GetPublicKey())

		if err != nil {
			log.Printf("Ошибка удаления книг из старых ответов: %v", err)
			return
		}

		deletedBooks, _ := result.RowsAffected()
		log.Printf("Удалено %d книг из старых ответов", deletedBooks)
	}
}
