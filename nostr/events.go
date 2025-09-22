// nostr/events.go
package nostr

import (
	"database/sql"
	"encoding/json"
	"log"
	"strings"
	"time"
	"turanga/config"

	"github.com/nbd-wtf/go-nostr"
)

// handleIncomingEvent обрабатывает входящие события Nostr (и запросы, и ответы).
func (sm *SubscriptionManager) handleIncomingEvent(event *nostr.Event) {
	cfg := config.GetConfig()
	switch event.Kind {
	case 8698:
		// Это запрос книги
		sm.handleIncomingBookRequest(event) // Вызываем существующую функцию обработки запросов
	case 8699:
		// Это ответ на запрос книги
		sm.handleIncomingBookResponse(event) // Новая функция для обработки ответов
	default:
		// Неизвестный kind, игнорируем или логируем
		if cfg.Debug {
			log.Printf("Получено событие неизвестного kind %d от %s", event.Kind, event.PubKey)
		}
		// Можно добавить логику для других kinds, если потребуется
	}
}

// handleIncomingBookRequest обрабатывает входящее событие запроса книги.
func (sm *SubscriptionManager) handleIncomingBookRequest(event *nostr.Event) {
	cfg := config.GetConfig()
	if sm.client != nil && sm.client.IsEnabled() && event.PubKey == sm.client.GetPublicKey() {
		if cfg.Debug {
			log.Printf("Игнорируем собственный запрос книги (kind: %d, ID: %s)", event.Kind, event.ID)
		}
		return
	}

	if cfg.Debug {
		log.Printf("Получен запрос книги через nostr (kind: %d, ID: %s) от pubkey: %s", event.Kind, event.ID, event.PubKey)
	}

	// 1. Проверяем черный список
	if sm.client != nil && sm.client.GetBlacklist() != nil {
		blacklist := sm.client.GetBlacklist()
		if blacklist.IsPubkeyBlocked(event.PubKey) {
			if cfg.Debug {
				log.Printf("Игнорируем запрос от заблокированного pubkey: %s", event.PubKey)
			}
			return
		}
	}

	// 2. Проверяем лимит запросов от этого пользователя с учетом бонусов
	if sm.cfg != nil && sm.cfg.MaxRequestsPerDay > 0 {
		// Получаем базовый лимит запросов
		baseLimit := sm.cfg.MaxRequestsPerDay

		// Проверяем, есть ли пользователь в друзьях и сколько у него бонусов
		friendBonus := 0
		var friendExists bool
		err := sm.db.QueryRow("SELECT EXISTS(SELECT 1 FROM friends WHERE pubkey = ?), COALESCE((SELECT download_count FROM friends WHERE pubkey = ?), 0)", event.PubKey, event.PubKey).Scan(&friendExists, &friendBonus)
		if err != nil {
			if cfg.Debug {
				log.Printf("Ошибка проверки бонусов для друга %s: %v", event.PubKey, err)
			}
		}

		// Рассчитываем эффективный лимит (базовый + бонусы)
		effectiveLimit := baseLimit + friendBonus

		requestCount, err := sm.getRequestCountForUser(event.PubKey, 24*time.Hour)
		if err != nil {
			if cfg.Debug {
				log.Printf("Ошибка проверки количества запросов от %s: %v", event.PubKey, err)
			}
		} else if requestCount >= effectiveLimit {
			if cfg.Debug {
				log.Printf("Игнорируем запрос от %s: превышен лимит (%d >= %d, базовый: %d, бонусы: %d)",
					event.PubKey, requestCount, effectiveLimit, baseLimit, friendBonus)
			}
			return
		} else {
			if cfg.Debug {
				log.Printf("Лимит запросов для %s: %d/%d (базовый: %d, бонусы: %d)",
					event.PubKey, requestCount, effectiveLimit, baseLimit, friendBonus)
			}
		}
	}

	// 3. Проверяем, не обрабатывали ли мы уже это событие
	var exists bool
	var isSent bool
	err := sm.db.QueryRow("SELECT EXISTS(SELECT 1 FROM nostr_book_requests WHERE event_id = ?), COALESCE(MAX(sent), 0) FROM nostr_book_requests WHERE event_id = ?", event.ID, event.ID).Scan(&exists, &isSent)
	if err != nil {
		if cfg.Debug {
			log.Printf("Ошибка проверки существования события %s в БД: %v", event.ID, err)
		}
		return
	}
	if exists {
		if isSent {
			if cfg.Debug {
				log.Printf("Ответ на запрос %s уже отправлен, игнорируем", event.ID)
			}
			return
		}
		if cfg.Debug {
			log.Printf("Событие %s уже обработано, пропускаем", event.ID)
		}
		return
	}

	// 4. Парсим содержимое события
	var requestData BookRequestEventContent // Используем существующую структуру
	if err := json.Unmarshal([]byte(event.Content), &requestData); err != nil {
		if cfg.Debug {
			log.Printf("Ошибка парсинга запроса nostr (ID: %s): %v", event.ID, err)
			log.Printf("Содержимое события (raw): %s", event.Content)
		}
		return
	}

	// 5. Валидация запроса
	// Проверяем, что хотя бы одно поле заполнено
	if requestData.Author == "" && requestData.Series == "" && requestData.Title == "" && requestData.FileHash == "" {
		if cfg.Debug {
			log.Printf("Игнорируем запрос %s: все поля пустые", event.ID)
		}
		return
	}

	// Проверяем минимальную длину для серии и названия (если заданы)
	if requestData.Series != "" && len(requestData.Series) < 4 {
		if cfg.Debug {
			log.Printf("Игнорируем запрос %s: серия '%s' короче 4 символов", event.ID, requestData.Series)
		}
		return
	}

	if requestData.Title != "" && len(requestData.Title) < 4 {
		if cfg.Debug {
			log.Printf("Игнорируем запрос %s: название '%s' короче 4 символов", event.ID, requestData.Title)
		}
		return
	}

	if cfg.Debug {
		log.Printf("Детали запроса: Автор='%s', Серия='%s', Название='%s', Хеш='%s', Источник='%s'",
			requestData.Author, requestData.Series, requestData.Title, requestData.FileHash, requestData.Source)
	}

	// 6. Сохраняем запрос в БД
	tx, err := sm.db.Begin()
	if err != nil {
		if cfg.Debug {
			log.Printf("Ошибка начала транзакции для сохранения запроса %s: %v", event.ID, err)
		}
		return
	}
	defer tx.Rollback() // Откат в случае ошибки

	result, err := tx.Exec(`
        INSERT INTO nostr_book_requests (event_id, pubkey, author, series, title, file_hash, created_at, processed, sent)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
    `, event.ID, event.PubKey, requestData.Author, requestData.Series, requestData.Title, requestData.FileHash, event.CreatedAt, false, false)
	if err != nil {
		if cfg.Debug {
			log.Printf("Ошибка сохранения запроса %s в БД: %v", event.ID, err)
		}
		return
	}

	requestID, err := result.LastInsertId()
	if err != nil {
		if cfg.Debug {
			log.Printf("Ошибка получения ID нового запроса %s: %v", event.ID, err)
		}
		return
	}

	// 7. Ищем книги в локальной БД по критериям запроса
	var bookIDs []int64
	query := "SELECT DISTINCT b.id FROM books b WHERE 1=1"
	args := []interface{}{}

	if requestData.Title != "" {
		query += " AND b.title LIKE ?"
		args = append(args, "%"+requestData.Title+"%")
	}
	if requestData.Series != "" {
		query += " AND b.series LIKE ?"
		args = append(args, "%"+requestData.Series+"%")
	}
	if requestData.Author != "" {
		query += " AND EXISTS (SELECT 1 FROM book_authors ba JOIN authors a ON ba.author_id = a.id WHERE ba.book_id = b.id AND a.last_name = ?)"
		args = append(args, requestData.Author)
	}
	if requestData.FileHash != "" {
		query += " AND b.file_hash = ?"
		args = append(args, requestData.FileHash)
	}

	rows, err := tx.Query(query, args...)
	if err != nil {
		if cfg.Debug {
			log.Printf("Ошибка поиска книг для запроса %s: %v", event.ID, err)
		}
	} else {
		defer rows.Close()
		for rows.Next() {
			var bookID int64
			if err := rows.Scan(&bookID); err == nil {
				bookIDs = append(bookIDs, bookID)
			}
		}
		if err = rows.Err(); err != nil {
			if cfg.Debug {
				log.Printf("Ошибка итерации по результатам поиска книг для запроса %s: %v", event.ID, err)
			}
		}
	}

	// 8. Сохраняем связи найденных книг с запросом
	for _, bookID := range bookIDs {
		var fileHash sql.NullString
		err := tx.QueryRow("SELECT file_hash FROM books WHERE id = ?", bookID).Scan(&fileHash)
		if err != nil && err != sql.ErrNoRows {
			if cfg.Debug {
				log.Printf("Ошибка получения file_hash для книги %d: %v", bookID, err)
			}
		}

		_, err = tx.Exec("INSERT OR IGNORE INTO nostr_request_books (request_id, book_id, file_hash) VALUES (?, ?, ?)", requestID, bookID, fileHash)
		if err != nil {
			if cfg.Debug {
				log.Printf("Ошибка сохранения связи книги %d с запросом %d: %v", bookID, requestID, err)
			}
		}
	}

	// 9. Помечаем запрос как обработанный
	_, err = tx.Exec("UPDATE nostr_book_requests SET processed = TRUE WHERE id = ?", requestID)
	if err != nil {
		if cfg.Debug {
			log.Printf("Ошибка обновления статуса запроса %d: %v", requestID, err)
		}
	}

	// 10. Фиксируем транзакцию
	err = tx.Commit()
	if err != nil {
		if cfg.Debug {
			log.Printf("Ошибка фиксации транзакции для запроса %s: %v", event.ID, err)
		}
		return
	}

	if cfg.Debug {
		log.Printf("Запрос %s успешно сохранен и обработан. Найдено книг: %d", event.ID, len(bookIDs))
	}

	// 11. Если найдены книги, отправляем ответ (пока заглушка)
	if len(bookIDs) > 0 {
		go sm.sendResponse(event.PubKey, bookIDs, event.ID) // Отправляем в фоне
	}
}

// handleIncomingBookResponse обрабатывает входящий ОТВЕТ на запрос книги (kind 8699).
func (sm *SubscriptionManager) handleIncomingBookResponse(event *nostr.Event) {
	var cfg = config.GetConfig()
	if sm.client != nil && sm.client.IsEnabled() && event.PubKey == sm.client.GetPublicKey() {
		if cfg.Debug {
			log.Printf("Игнорируем собственный ответ на запрос книги (kind: %d, ID: %s)", event.Kind, event.ID)
		}
		return
	}
	if cfg.Debug {
		log.Printf("Получен ОТВЕТ на запрос книги через Nostr (kind: %d, ID: %s) от pubkey: %s", event.Kind, event.ID, event.PubKey)
	}

	// 1. Парсим содержимое события (ожидаем JSON-массив BookResponseData)
	var responseData []BookResponseData
	if err := json.Unmarshal([]byte(event.Content), &responseData); err != nil {
		if cfg.Debug {
			log.Printf("Ошибка парсинга ОТВЕТА Nostr (ID: %s): %v", event.ID, err)
			log.Printf("Содержимое события (raw): %s", event.Content)
		}
		return
	}

	// 2. Извлекаем информацию о запросе из тегов события ответа (NIP-10)
	var requestEventID string

	// Извлекаем информацию из тегов
	for _, tag := range event.Tags {
		if len(tag) >= 2 {
			switch tag[0] {
			case "e":
				// Тег "e" обычно содержит ID события, на которое отвечают
				// NIP-10 рекомендует использовать маркер "reply" для ответов
				if len(tag) >= 4 && tag[3] == "reply" {
					requestEventID = tag[1]
				} else if requestEventID == "" { // fallback, если маркер не указан
					requestEventID = tag[1]
				}
			case "p":
				// Тег "p" обычно содержит публичный ключ адресата
				_ = tag[1] // Заглушка, чтобы компилятор не ругался на неиспользуемую переменную tag[1]
			}
		}
	}

	if requestEventID == "" {
		if cfg.Debug {
			log.Printf("Предупреждение: Не удалось извлечь ID исходного запроса из тегов ответа %s", event.ID)
		}
	}

	if cfg.Debug {
		log.Printf("Детали ОТВЕТА: Найдено книг: %d, Ответ на запрос: %s, Отправитель ответа: %s",
			len(responseData), requestEventID, event.PubKey)
	}

	// Проверяем черный список перед обработкой
	if sm.client != nil && sm.client.GetBlacklist() != nil {
		blacklist := sm.client.GetBlacklist()

		// Проверяем публичный ключ отправителя
		if blacklist.IsPubkeyBlocked(event.PubKey) {
			if cfg.Debug {
				log.Printf("Игнорируем ответ от заблокированного pubkey: %s", event.PubKey)
			}
			return
		}

		// Проверяем file hashes в содержимом ответа
		for _, book := range responseData {
			if book.FileHash != "" && blacklist.IsFileHashBlocked(book.FileHash) {
				if cfg.Debug {
					log.Printf("Игнорируем ответ, содержащий заблокированный file_hash: %s", book.FileHash)
				}
				return
			}
		}
	}

	// Проверяем, является ли это ответом на наш запрос
	var isOurRequest bool
	if requestEventID != "" {
		err := sm.db.QueryRow("SELECT EXISTS(SELECT 1 FROM nostr_book_requests WHERE event_id = ?)", requestEventID).Scan(&isOurRequest)
		if err != nil {
			if cfg.Debug {
				log.Printf("Ошибка проверки, является ли запрос %s нашим: %v", requestEventID, err)
			}
		}
	}

	if isOurRequest {
		//		if cfg.Debug {
		log.Printf("Получен ответ на НАШ запрос %s от %s", requestEventID, event.PubKey)
		//		}
		// Здесь можно добавить специальную обработку для ответов на наши запросы
	} else {
		if cfg.Debug {
			log.Printf("Получен ответ на чужой запрос %s от %s", requestEventID, event.PubKey)
		}
	}

	// 3. Проверяем, не обрабатывали ли мы уже этот ответ (по event.ID)
	var exists bool
	var isProcessed bool
	err := sm.db.QueryRow("SELECT EXISTS(SELECT 1 FROM nostr_received_responses WHERE event_id = ?), COALESCE(MAX(processed), 0) FROM nostr_received_responses WHERE event_id = ?", event.ID, event.ID).Scan(&exists, &isProcessed)
	if err != nil {
		if cfg.Debug {
			log.Printf("Ошибка проверки существования ответа %s в БД: %v", event.ID, err)
		}
	}
	if exists {
		if isProcessed {
			if cfg.Debug {
				log.Printf("Ответ %s уже обработан, пропускаем", event.ID)
			}
			return
		}
		if cfg.Debug {
			log.Printf("Ответ %s уже сохранен в БД, но не обработан", event.ID)
		}
		// Можно продолжить обработку, если нужно
	}

	// 4. Сохраняем полученный ответ в БД
	tx, err := sm.db.Begin()
	if err != nil {
		if cfg.Debug {
			log.Printf("Ошибка начала транзакции для сохранения ответа %s: %v", event.ID, err)
		}
		return
	}
	defer tx.Rollback() // Откат в случае ошибки

	result, err := tx.Exec(`
		INSERT INTO nostr_received_responses (event_id, responder_pubkey, request_event_id, received_at, content, processed)
		VALUES (?, ?, ?, ?, ?, ?)
	`, event.ID, event.PubKey, requestEventID, event.CreatedAt, event.Content, false)
	if err != nil {
		if cfg.Debug {
			log.Printf("Ошибка сохранения ответа %s в БД: %v", event.ID, err)
		}
		return
	}

	responseID, err := result.LastInsertId()
	if err != nil {
		if cfg.Debug {
			log.Printf("Ошибка получения ID нового ответа %s: %v", event.ID, err)
		}
		responseID = 0
	}

	// 5. Сохраняем книги из ответа отдельно с проверкой уникальности file_hash
	bookIDsFromResponse := make([]int64, 0, len(responseData))
	receivedFileHashes := make(map[string]bool) // Для отслеживания полученных хешей

	for _, book := range responseData {
		// Проверяем, существует ли уже такая книга в ЭТОМ ответе (а не вообще в БД)
		if book.FileHash != "" {
			var exists bool
			err := tx.QueryRow("SELECT EXISTS(SELECT 1 FROM nostr_response_books WHERE file_hash = ? AND response_id = ?)", book.FileHash, responseID).Scan(&exists)
			if err != nil {
				if cfg.Debug {
					log.Printf("Ошибка проверки существования книги с file_hash %s в этом ответе: %v", book.FileHash, err)
				}
				continue
			}
			if exists {
				if cfg.Debug {
					log.Printf("Книга с file_hash %s уже существует в этом ответе, пропускаем", book.FileHash)
				}
				continue
			}
		}

		// Сериализуем книгу обратно в JSON для хранения в raw_data
		bookJSON, jsonErr := json.Marshal(book)
		if jsonErr != nil {
			if cfg.Debug {
				log.Printf("Предупреждение: Ошибка сериализации книги в JSON для ответа %s: %v", event.ID, jsonErr)
			}
			bookJSON = []byte("{}")
		}

		// Преобразуем slice авторов в строку
		authorsStr := strings.Join(book.Authors, ", ")

		// Вставляем данные книги в таблицу nostr_response_books
		bookResult, bookErr := tx.Exec(`
			INSERT INTO nostr_response_books 
			(response_id, book_id, title, authors, series, series_number, file_type, file_hash, file_size, ipfs_cid, raw_data)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, responseID, sql.NullInt64{}, book.Title, authorsStr, book.Series, book.SeriesNumber, book.FileType, book.FileHash, book.FileSize, book.IPFSCID, string(bookJSON))

		if bookErr != nil {
			if cfg.Debug {
				log.Printf("Ошибка сохранения книги из ответа (title: %s, file_hash: %s): %v", book.Title, book.FileHash, bookErr)
			}
		} else {
			bookID, bookIDErr := bookResult.LastInsertId()
			if bookIDErr != nil {
				if cfg.Debug {
					log.Printf("Ошибка получения ID новой записи книги из ответа (response_id: %d): %v", responseID, bookIDErr)
				}
			} else {
				bookIDsFromResponse = append(bookIDsFromResponse, bookID)
				if book.FileHash != "" {
					receivedFileHashes[book.FileHash] = true
				}
			}
		}
	}

	// 6. Фиксируем транзакцию
	err = tx.Commit()
	if err != nil {
		if cfg.Debug {
			log.Printf("Ошибка фиксации транзакции для ответа %s: %v", event.ID, err)
		}
		return
	}

	if cfg.Debug {
		log.Printf("Ответ %s успешно сохранен в БД (ID записи: %d)", event.ID, responseID)
	}

	if len(bookIDsFromResponse) > 0 {
		if cfg.Debug {
			log.Printf("  Сохранено %d книг из ответа. ID записей: %v", len(bookIDsFromResponse), bookIDsFromResponse)
		}
	} else {
		if cfg.Debug {
			log.Printf("  Не сохранено ни одной новой книги из ответа (возможно, все уже существуют)")
		}
	}

	// 7. Обрабатываем полученные данные
	for i, book := range responseData {
		if cfg.Debug {
			log.Printf("  Книга %d из ответа: ID=%d, Название='%s', Авторы=%v, Формат=%s, FileHash=%s",
				i+1, book.ID, book.Title, book.Authors, book.FileType, book.FileHash)
		}
	}

	// 8. Помечаем ответ как обработанный
	_, err = sm.db.Exec("UPDATE nostr_received_responses SET processed = TRUE WHERE event_id = ?", event.ID)
	if err != nil {
		if cfg.Debug {
			log.Printf("Ошибка обновления статуса ответа %s: %v", event.ID, err)
		}
	}

	// 9. Добавляем или обновляем информацию о друге
	if event.PubKey != "" {
		now := time.Now().Unix()
		_, err = sm.db.Exec(`
			INSERT INTO friends (pubkey, download_count, last_download_at, created_at, updated_at)
			VALUES (?, 0, 0, ?, ?)
			ON CONFLICT(pubkey) DO UPDATE SET updated_at = ?
		`, event.PubKey, now, now, now)
		if err != nil {
			if cfg.Debug {
				log.Printf("Ошибка обновления информации о друге %s: %v", event.PubKey, err)
			}
		} else {
			if cfg.Debug {
				log.Printf("Информация о друге %s обновлена", event.PubKey)
			}
		}
	}

	// 10. Вычеркиваем книги из подготовленных к ответу
	if len(receivedFileHashes) > 0 && requestEventID != "" {
		sm.removeBooksFromPendingResponse(requestEventID, receivedFileHashes)
	}
}

// CleanupOldEvents удаляет устаревшие события
func (sm *SubscriptionManager) CleanupOldEvents() {
	var cfg = config.GetConfig()
	now := time.Now()
	// Удаляем входящие запросы старше 12 часов
	cutoffIncoming := now.Add(-12 * time.Hour).Unix()

	if cfg.Debug {
		log.Printf("Очистка устаревших событий (старше 12 часов: %d)", cutoffIncoming)
	}

	tx, err := sm.db.Begin()
	if err != nil {
		if cfg.Debug {
			log.Printf("Ошибка начала транзакции для очистки: %v", err)
		}
		return
	}
	defer tx.Rollback()

	// Удаляем устаревшие входящие запросы (и связанные данные)
	result, err := tx.Exec(`
        DELETE FROM nostr_book_requests 
        WHERE created_at < ? AND pubkey != ?
    `, cutoffIncoming, sm.client.GetPublicKey())
	if err != nil {
		if cfg.Debug {
			log.Printf("Ошибка удаления устаревших входящих запросов: %v", err)
		}
		return
	}
	deletedIncoming, _ := result.RowsAffected()

	// Удаляем устаревшие связи запросов с книгами
	result, err = tx.Exec(`
        DELETE FROM nostr_request_books 
        WHERE request_id IN (
            SELECT id FROM nostr_book_requests 
            WHERE created_at < ? AND pubkey != ?
        )
    `, cutoffIncoming, sm.client.GetPublicKey())
	if err != nil {
		if cfg.Debug {
			log.Printf("Ошибка удаления устаревших связей запросов: %v", err)
		}
		return
	}
	deletedRequestBooks, _ := result.RowsAffected()

	// Удаляем устаревшие ответы на чужие запросы
	result, err = tx.Exec(`
        DELETE FROM nostr_received_responses 
        WHERE received_at < ? 
        AND request_event_id IN (
            SELECT event_id FROM nostr_book_requests WHERE pubkey != ? AND created_at < ?
        )
    `, cutoffIncoming, sm.client.GetPublicKey(), cutoffIncoming)
	if err != nil {
		if cfg.Debug {
			log.Printf("Ошибка удаления устаревших ответов на чужие запросы: %v", err)
		}
		return
	}
	deletedResponses, _ := result.RowsAffected()

	err = tx.Commit()
	if err != nil {
		if cfg.Debug {
			log.Printf("Ошибка фиксации транзакции очистки: %v", err)
		}
		return
	}

	if cfg.Debug {
		log.Printf("Очистка устаревших событий завершена: %d входящих запросов, %d связей, %d ответов",
			deletedIncoming, deletedRequestBooks, deletedResponses)
	}
}
