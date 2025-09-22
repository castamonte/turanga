// web/request.go
package web

import (
	"context"
	"database/sql"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"turanga/config"
)

// ResponseBook представляет книгу из ответа
type ResponseBook struct {
	ID              int64
	Title           string
	Authors         string
	Series          string
	SeriesNumber    string
	FileType        string
	FileSize        int64
	FileHash        string
	IPFSCID         string
	IsLocal         bool
	LocalID         sql.NullInt64
	ResponderPubkey string
}

// ResponseGroup группа ответов по серии
type ResponseGroup struct {
	Series   string
	Books    []ResponseBook
	HasLocal bool
}

// Структура для информации о запросе
type RequestInfo struct {
	Author   string
	Series   string
	Title    string
	FileHash string
}

// ShowRequestFormHandler отображает форму запроса книги через Nostr или ответы на активные запросы
func (w *WebInterface) ShowRequestFormHandler(wr http.ResponseWriter, r *http.Request) {
	cfg := config.GetConfig()

	if cfg.Debug {
		log.Printf("ShowRequestFormHandler called: method=%s, path=%s", r.Method, r.URL.Path)
	}

	// Проверяем аутентификацию
	if !w.isAuthenticated(r) {
		log.Printf("Request form access attempt by unauthorized user")
		http.Error(wr, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Принимаем только GET запросы
	if r.Method != http.MethodGet {
		log.Printf("Method not allowed for request form: %s", r.Method)
		http.Error(wr, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Проверяем параметр new для принудительного показа формы
	if r.URL.Query().Get("new") == "1" {
		w.showRequestForm(wr, r)
		return
	}

	// Проверяем, есть ли отправленные запросы
	hasActiveRequests, err := w.hasActiveRequests()
	if err != nil {
		log.Printf("Error checking active requests: %v", err)
		http.Error(wr, "Database error", http.StatusInternalServerError)
		return
	}

	if hasActiveRequests {
		// Показываем ответы на активные запросы
		w.showResponsesForActiveRequests(wr, r)
		return
	}

	// Показываем форму запроса
	w.showRequestForm(wr, r)
}

// getActiveRequestResponses получает ответы на НАШИ отправленные запросы, сгруппированные по сериям
// Исключает книги, которые находятся в чёрном списке
func (w *WebInterface) getActiveRequestResponses() ([]ResponseGroup, error) {
	cfg := config.GetConfig()

	// Проверяем, есть ли у нас Nostr клиент
	if w.NostrClient == nil {
		return []ResponseGroup{}, nil
	}

	// Получаем наш публичный ключ
	ourPubkey := w.NostrClient.GetPublicKey()
	if ourPubkey == "" {
		return []ResponseGroup{}, nil
	}

	// Получаем чёрный список для фильтрации
	blacklist := w.NostrClient.GetBlacklist()
	var blockedHashes []string
	if blacklist != nil {
		blockedHashes = blacklist.GetAllBlockedFileHashes()
	}

	// Формируем базовый запрос
	query := `
        SELECT DISTINCT
            nrb.id,
            nrb.title,
            nrb.authors,
            nrb.series,
            nrb.series_number,
            nrb.file_type,
            nrb.file_size,
            nrb.file_hash,
            nrb.ipfs_cid,
            CASE WHEN b.id IS NOT NULL THEN 1 ELSE 0 END as is_local,
            b.id as local_id, -- <-- Убедимся, что это поле заполняется правильно
            nrr.responder_pubkey
        FROM nostr_response_books nrb
        JOIN nostr_received_responses nrr ON nrb.response_id = nrr.id
        LEFT JOIN books b ON nrb.file_hash = b.file_hash -- <-- LEFT JOIN для получения ID локальной книги
        WHERE nrr.request_event_id IN (
            SELECT event_id FROM nostr_book_requests 
            WHERE sent = 1 AND pubkey = ?
        )
    `

	args := []interface{}{ourPubkey}

	// Добавляем фильтрацию по чёрному списку
	if len(blockedHashes) > 0 {
		placeholders := make([]string, len(blockedHashes))
		for i, hash := range blockedHashes {
			placeholders[i] = "?"
			args = append(args, hash)
		}
		query += " AND nrb.file_hash NOT IN (" + strings.Join(placeholders, ",") + ")"
	}

	query += ` ORDER BY nrb.series, nrb.series_number, nrb.title`

	rows, err := w.db.Query(query, args...)
	if err != nil {
		log.Printf("Database query error: %v", err)
		log.Printf("Query: %s", query)
		log.Printf("Args count: %d", len(args))
		return nil, err
	}
	defer rows.Close()

	// Группируем по сериям
	seriesMap := make(map[string][]ResponseBook)
	count := 0
	for rows.Next() {
		count++
		var book ResponseBook
		var isLocal int
		var localID sql.NullInt64 // <-- Используем sql.NullInt64
		var responderPubkey string
		err := rows.Scan(
			&book.ID,
			&book.Title,
			&book.Authors,
			&book.Series,
			&book.SeriesNumber,
			&book.FileType,
			&book.FileSize,
			&book.FileHash,
			&book.IPFSCID,
			&isLocal,
			&localID, // <-- Сканируем в sql.NullInt64
			&responderPubkey,
		)
		if err != nil {
			if cfg.Debug {
				log.Printf("Scan error: %v", err)
			}
			return nil, err
		}
		book.IsLocal = isLocal == 1
		book.LocalID = localID // <-- Присваиваем отсканированное значение
		book.ResponderPubkey = responderPubkey

		seriesKey := book.Series
		if seriesKey == "" {
			seriesKey = "Без серии"
		}

		seriesMap[seriesKey] = append(seriesMap[seriesKey], book)
	}

	if cfg.Debug {
		log.Printf("Found %d books in response for pubkey %s (after blacklist filter)", count, ourPubkey)
	}

	// Преобразуем в группы
	var groups []ResponseGroup
	for series, books := range seriesMap {
		hasLocal := false
		for _, book := range books {
			if book.IsLocal {
				hasLocal = true
				break
			}
		}

		groups = append(groups, ResponseGroup{
			Series:   series,
			Books:    books,
			HasLocal: hasLocal,
		})
	}

	return groups, nil
}

// getLastRequestID получает ID последнего нашего отправленного запроса
func (w *WebInterface) getLastRequestID() (int64, error) {
	var id int64
	err := w.db.QueryRow(`
        SELECT id 
        FROM nostr_book_requests 
        WHERE sent = 1 AND pubkey = ? 
        ORDER BY created_at DESC 
        LIMIT 1
    `, w.NostrClient.GetPublicKey()).Scan(&id)
	if err != nil {
		return 0, err
	}
	return id, nil
}

// showRequestForm показывает форму для нового запроса
func (w *WebInterface) showRequestForm(wr http.ResponseWriter, r *http.Request) {
	cfg := config.GetConfig()

	// Подготавливаем базовые данные для шаблона
	data := struct {
		IsAuthenticated bool
		CatalogTitle    string
		AppTitle        string
		Success         bool
		ShowResponses   bool
		Query           string
		RequestInfo     RequestInfo
	}{
		IsAuthenticated: w.isAuthenticated(r),
		CatalogTitle:    w.config.GetCatalogTitle(),
		AppTitle:        w.appTitle,
		Success:         r.URL.Query().Get("success") == "1",
		ShowResponses:   false,
		Query:           "",
		RequestInfo:     RequestInfo{},
	}

	// Загружаем и выполняем шаблон
	tmplPath := filepath.Join(w.rootPath, "web", "templates", "request.html") // <-- Абсолютный путь
	tmpl, err := template.New("request.html").Funcs(template.FuncMap{
		"sub":        func(a, b int) int { return a - b },
		"split":      strings.Split,
		"trim":       strings.TrimSpace,
		"urlquery":   url.QueryEscape,
		"formatSize": FormatFileSize,
	}).ParseFiles(tmplPath) // <-- Используем tmplPath
	if err != nil {
		log.Printf("Error parsing request template: %v", err)
		http.Error(wr, "Template error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	wr.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(wr, "request", data); err != nil {
		log.Printf("Error executing request template: %v", err)
		http.Error(wr, "Internal Server Error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if cfg.Debug {
		log.Printf("Request form rendered successfully")
	}
}

func (w *WebInterface) showResponsesForActiveRequests(wr http.ResponseWriter, r *http.Request) {
	cfg := config.GetConfig()

	// Получаем ответы на активные запросы
	responseGroups, err := w.getActiveRequestResponses()
	if err != nil {
		log.Printf("Error getting responses for active requests: %v", err)
		http.Error(wr, "Database error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Получаем информацию о последнем запросе
	requestInfo, err := w.getLastRequestInfo()
	if err != nil {
		log.Printf("Error getting last request info: %v", err)
		// Не критично, продолжаем без информации о запросе
		requestInfo = RequestInfo{}
	}

	// Подготавливаем данные для шаблона
	data := struct {
		IsAuthenticated bool
		CatalogTitle    string
		AppTitle        string
		ShowResponses   bool
		ResponseGroups  []ResponseGroup
		RequestInfo     RequestInfo
		Query           string
	}{
		IsAuthenticated: w.isAuthenticated(r),
		CatalogTitle:    w.config.GetCatalogTitle(),
		AppTitle:        w.appTitle,
		ShowResponses:   true,
		ResponseGroups:  responseGroups,
		RequestInfo:     requestInfo,
		Query:           "",
	}

	// Загружаем и выполняем шаблон
	tmplPath := filepath.Join(w.rootPath, "web", "templates", "request.html") // <-- Абсолютный путь
	tmpl, err := template.New("request.html").Funcs(template.FuncMap{
		"sub":        func(a, b int) int { return a - b },
		"split":      strings.Split,
		"trim":       strings.TrimSpace,
		"urlquery":   url.QueryEscape,
		"formatSize": FormatFileSize,
	}).ParseFiles(tmplPath) // <-- Используем tmplPath
	if err != nil {
		log.Printf("Error parsing request template: %v", err)
		http.Error(wr, "Template error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	wr.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(wr, "request", data); err != nil {
		log.Printf("Error executing request template: %v", err)
		http.Error(wr, "Internal Server Error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if cfg.Debug {
		log.Printf("Responses for active requests rendered successfully")
	}
}

// HandleRequestFormHandler обрабатывает отправку формы запроса книги
func (w *WebInterface) HandleRequestFormHandler(wr http.ResponseWriter, r *http.Request) {
	cfg := config.GetConfig()

	if cfg.Debug {
		log.Printf("HandleRequestFormHandler called: method=%s, path=%s", r.Method, r.URL.Path)
	}

	// Проверяем аутентификацию
	if !w.isAuthenticated(r) {
		log.Printf("Request form submission attempt by unauthorized user")
		http.Error(wr, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Принимаем только POST запросы
	if r.Method != http.MethodPost {
		log.Printf("Method not allowed for request form submission: %s", r.Method)
		http.Error(wr, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Получаем данные формы
	author := strings.TrimSpace(r.FormValue("author"))
	series := strings.TrimSpace(r.FormValue("series"))
	title := strings.TrimSpace(r.FormValue("title"))
	fileHash := strings.TrimSpace(r.FormValue("file_hash"))

	if cfg.Debug {
		log.Printf("Received request form data - Author: '%s', Series: '%s', Title: '%s', FileHash: '%s'",
			author, series, title, fileHash)
	}

	// Валидация запроса
	// Проверяем, что хотя бы одно поле заполнено
	if author == "" && series == "" && title == "" && fileHash == "" {
		log.Printf("Empty request form data")
		http.Error(wr, "Заполните хотя бы одно поле", http.StatusBadRequest)
		return
	}

	// Проверяем минимальную длину для серии и названия (если заданы)
	if series != "" && len(series) < 4 {
		log.Printf("Series too short: '%s'", series)
		http.Error(wr, "Серия должна быть не менее 4 символов", http.StatusBadRequest)
		return
	}

	if title != "" && len(title) < 4 {
		log.Printf("Title too short: '%s'", title)
		http.Error(wr, "Название должно быть не менее 4 символов", http.StatusBadRequest)
		return
	}

	// Проверяем формат хеша (если задан)
	if fileHash != "" {
		// Хеш должен быть длиной 16 символов и содержать только [a-f0-9]
		if len(fileHash) != 16 {
			log.Printf("Invalid file hash length: %d", len(fileHash))
			http.Error(wr, "Хеш файла должен быть длиной 16 символов", http.StatusBadRequest)
			return
		}

		// Проверяем, что хеш содержит только допустимые символы
		valid := true
		for _, c := range fileHash {
			if !((c >= 'a' && c <= 'f') || (c >= '0' && c <= '9')) {
				valid = false
				break
			}
		}

		if !valid {
			log.Printf("Invalid file hash characters: '%s'", fileHash)
			http.Error(wr, "Хеш файла должен содержать только символы a-f и 0-9", http.StatusBadRequest)
			return
		}
	}

	// Проверяем, инициализирован ли Nostr клиент
	if w.NostrClient == nil {
		log.Println("Nostr клиент не инициализирован, публикация запроса книги пропущена")
		http.Error(wr, "Интеграция с Nostr не настроена", http.StatusServiceUnavailable)
		return
	}

	// Выполняем очистку перед отправкой нового запроса
	if err := w.cleanupOldNostrRequests(); err != nil {
		if cfg.Debug {
			log.Printf("Предупреждение: ошибка очистки старых запросов: %v", err)
		}
		// Продолжаем выполнение, ошибка очистки не критична
	}

	if err := w.cleanupOrphanedNostrResponses(); err != nil {
		if cfg.Debug {
			log.Printf("Предупреждение: ошибка очистки orphaned ответов: %v", err)
		}
		// Продолжаем выполнение, ошибка очистки не критична
	}

	// Создаем контекст с таймаутом для публикации
	pubCtx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	// Публикуем событие запроса через Nostr клиент
	err := w.NostrClient.PublishBookRequestEvent(pubCtx, author, series, title, fileHash)
	if err != nil {
		log.Printf("Ошибка публикации запроса книги через Nostr: %v", err)
		http.Error(wr, "Ошибка отправки запроса в сеть Nostr: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if cfg.Debug {
		log.Printf("Запрос книги через Nostr успешно отправлен: Автор='%s', Серия='%s', Название='%s', Хеш='%s'",
			author, series, title, fileHash)
	}

	// Перенаправляем на страницу с ответами
	http.Redirect(wr, r, "/request", http.StatusSeeOther)
}

// getLastRequestInfo получает информацию о последнем НАШЕМ запросе
func (w *WebInterface) getLastRequestInfo() (RequestInfo, error) {
	var info RequestInfo
	if w.NostrClient == nil {
		return info, fmt.Errorf("nostr client not initialized")
	}

	ourPubkey := w.NostrClient.GetPublicKey()
	if ourPubkey == "" {
		return info, fmt.Errorf("nostr pubkey not available")
	}

	err := w.db.QueryRow(`
        SELECT author, series, title, file_hash 
        FROM nostr_book_requests 
        WHERE sent = 1 AND pubkey = ?
        ORDER BY created_at DESC 
        LIMIT 1
    `, ourPubkey).Scan(&info.Author, &info.Series, &info.Title, &info.FileHash)

	if err != nil {
		return RequestInfo{}, err
	}

	return info, nil
}

// hasActiveRequests проверяет, есть ли отправленные запросы (для показа ответов)
func (w *WebInterface) hasActiveRequests() (bool, error) {
	// Показываем ответы, если есть хоть один отправленный запрос
	var count int
	err := w.db.QueryRow("SELECT COUNT(*) FROM nostr_book_requests WHERE sent = 1").Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// CheckUpdatesHandler проверяет наличие новых данных
func (w *WebInterface) CheckUpdatesHandler(wr http.ResponseWriter, r *http.Request) {
	// Проверяем аутентификацию
	if !w.isAuthenticated(r) {
		w.writeJSONResponse(wr, map[string]interface{}{
			"hasNewData": false,
			"error":      "Unauthorized",
		})
		return
	}

	// Получаем время последнего ответа
	var lastResponseTime int64
	err := w.db.QueryRow(`
        SELECT COALESCE(MAX(received_at), 0) 
        FROM nostr_received_responses 
        WHERE request_event_id IN (
            SELECT event_id FROM nostr_book_requests WHERE sent = 1
        )
    `).Scan(&lastResponseTime)

	if err != nil {
		w.writeJSONResponse(wr, map[string]interface{}{
			"hasNewData": false,
			"error":      err.Error(),
		})
		return
	}

	// Сравниваем с временем последнего обновления (передается в параметрах)
	lastCheck := r.URL.Query().Get("last_check")
	if lastCheck != "" {
		if lastCheckTime, err := strconv.ParseInt(lastCheck, 10, 64); err == nil {
			if lastResponseTime > lastCheckTime {
				w.writeJSONResponse(wr, map[string]interface{}{
					"hasNewData":       true,
					"lastResponseTime": lastResponseTime,
				})
				return
			}
		}
	}

	w.writeJSONResponse(wr, map[string]interface{}{
		"hasNewData":       false,
		"lastResponseTime": lastResponseTime,
	})
}

// CheckUpdatesDetailedHandler детальная проверка обновлений
func (w *WebInterface) CheckUpdatesDetailedHandler(wr http.ResponseWriter, r *http.Request) {
	// Проверяем аутентификацию
	if !w.isAuthenticated(r) {
		w.writeJSONResponse(wr, map[string]interface{}{
			"updated": false,
			"error":   "Unauthorized",
		})
		return
	}

	// Получаем количество ответов
	var currentCount int
	err := w.db.QueryRow(`
        SELECT COUNT(*) 
        FROM nostr_received_responses 
        WHERE request_event_id IN (
            SELECT event_id FROM nostr_book_requests WHERE sent = 1
        )
    `).Scan(&currentCount)

	if err != nil {
		w.writeJSONResponse(wr, map[string]interface{}{
			"updated": false,
			"error":   err.Error(),
		})
		return
	}

	// Сравниваем с предыдущим количеством (передается в параметрах)
	prevCountStr := r.URL.Query().Get("prev_count")
	if prevCountStr != "" {
		if prevCount, err := strconv.Atoi(prevCountStr); err == nil {
			if currentCount != prevCount {
				w.writeJSONResponse(wr, map[string]interface{}{
					"updated":       true,
					"currentCount":  currentCount,
					"previousCount": prevCount,
				})
				return
			}
		}
	}

	w.writeJSONResponse(wr, map[string]interface{}{
		"updated":      false,
		"currentCount": currentCount,
	})
}

// ResponseCountHandler возвращает количество полученных ответов
func (w *WebInterface) ResponseCountHandler(wr http.ResponseWriter, r *http.Request) {
	// Проверяем аутентификацию
	if !w.isAuthenticated(r) {
		w.writeJSONResponse(wr, map[string]interface{}{
			"count": -1,
			"error": "Unauthorized",
		})
		return
	}

	// Получаем количество ответов на наши запросы
	var count int
	err := w.db.QueryRow(`
        SELECT COUNT(*) 
        FROM nostr_received_responses 
        WHERE request_event_id IN (
            SELECT event_id FROM nostr_book_requests 
            WHERE sent = 1 AND pubkey = ?
        )
    `, w.NostrClient.GetPublicKey()).Scan(&count)

	if err != nil {
		w.writeJSONResponse(wr, map[string]interface{}{
			"count": -1,
			"error": err.Error(),
		})
		return
	}

	w.writeJSONResponse(wr, map[string]interface{}{
		"count": count,
	})
}

// cleanupOldNostrRequests удаляет старые запросы (старше 24 часов)
func (w *WebInterface) cleanupOldNostrRequests() error {
	cfg := config.GetConfig()

	// Удаляем запросы старше 24 часов
	result, err := w.db.Exec(`
        DELETE FROM nostr_book_requests 
        WHERE created_at < datetime('now', '-1 day')
    `)
	if err != nil {
		return fmt.Errorf("ошибка удаления старых запросов: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected > 0 {
		if cfg.Debug {
			log.Printf("Удалено старых Nostr запросов: %d", rowsAffected)
		}
	}

	return nil
}

// cleanupOrphanedNostrResponses удаляет ответы, не связанные с текущими запросами
func (w *WebInterface) cleanupOrphanedNostrResponses() error {
	cfg := config.GetConfig()

	// Получаем ID последнего запроса (если есть)
	var lastRequestEventID sql.NullString
	err := w.db.QueryRow(`
        SELECT event_id 
        FROM nostr_book_requests 
        WHERE sent = 1 
        ORDER BY created_at DESC 
        LIMIT 1
    `).Scan(&lastRequestEventID)

	if err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("ошибка получения последнего запроса: %w", err)
	}

	// Если есть последний запрос, удаляем все ответы, кроме связанных с ним
	if lastRequestEventID.Valid {
		result, err := w.db.Exec(`
            DELETE FROM nostr_received_responses 
            WHERE request_event_id != ?
        `, lastRequestEventID.String)
		if err != nil {
			return fmt.Errorf("ошибка удаления старых ответов: %w", err)
		}

		rowsAffected, _ := result.RowsAffected()
		if rowsAffected > 0 {
			if cfg.Debug {
				log.Printf("Удалено orphaned Nostr ответов: %d", rowsAffected)
			}
		}

		// Также удаляем книги-ответы, которые больше не связаны с ответами
		result, err = w.db.Exec(`
            DELETE FROM nostr_response_books 
            WHERE response_id NOT IN (
                SELECT id FROM nostr_received_responses
            )
        `)
		if err != nil {
			return fmt.Errorf("ошибка удаления orphaned книг-ответов: %w", err)
		}

		rowsAffected, _ = result.RowsAffected()
		if rowsAffected > 0 {
			if cfg.Debug {
				log.Printf("Удалено orphaned Nostr книг-ответов: %d", rowsAffected)
			}
		}
	} else {
		// Если нет активных запросов, удаляем все ответы и книги-ответы
		result, err := w.db.Exec(`DELETE FROM nostr_response_books`)
		if err != nil {
			return fmt.Errorf("ошибка удаления всех книг-ответов: %w", err)
		}
		rowsAffected, _ := result.RowsAffected()
		if rowsAffected > 0 {
			if cfg.Debug {
				log.Printf("Удалено всех Nostr книг-ответов: %d", rowsAffected)
			}
		}

		result, err = w.db.Exec(`DELETE FROM nostr_received_responses`)
		if err != nil {
			return fmt.Errorf("ошибка удаления всех ответов: %w", err)
		}
		rowsAffected, _ = result.RowsAffected()
		if rowsAffected > 0 {
			if cfg.Debug {
				log.Printf("Удалено всех Nostr ответов: %d", rowsAffected)
			}
		}
	}

	return nil
}

// cleanupAllNostrData полностью очищает таблицы Nostr (для ревизии)
func (w *WebInterface) cleanupAllNostrData() error {
	tables := []string{
		"nostr_response_books",
		"nostr_request_books",
		"nostr_received_responses",
		"nostr_book_requests",
	}

	for _, table := range tables {
		result, err := w.db.Exec(`DELETE FROM ` + table)
		if err != nil {
			return fmt.Errorf("ошибка очистки таблицы %s: %w", table, err)
		}

		rowsAffected, _ := result.RowsAffected()
		if rowsAffected > 0 {
			log.Printf("Очищена таблица %s: удалено %d записей", table, rowsAffected)
		}
	}

	return nil
}
