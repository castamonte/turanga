// web/web.go
package web

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"turanga/config"
	"turanga/models"
	"turanga/scanner"
)

// ShowWebInterface обрабатывает запросы к главной странице
func (w *WebInterface) ShowWebInterface(wr http.ResponseWriter, r *http.Request) {

	var totalBooks int
	var rows *sql.Rows
	var err error

	// Получаем параметры пагинации и поиска из URL
	pageStr := r.URL.Query().Get("page")
	queryStr := r.URL.Query().Get("q") // <-- Получаем поисковый запрос
	revisionSuccess := r.URL.Query().Get("revision") == "1"

	page := 1
	if pageStr != "" {
		p, err := strconv.Atoi(pageStr)
		if err == nil && p > 0 {
			page = p
		}
	}

	// Количество книг на странице - используем значение из конфигурации
	perPage := 60 // Значение по умолчанию
	if w.config != nil {
		// Используем PaginationThreshold из конфига для веб-интерфейса тоже
		perPage = w.config.PaginationThreshold
		//		log.Printf("Используем порог пагинации из конфигурации для веб-интерфейса: %d", perPage)
	} else {
		//		log.Printf("Конфигурация не доступна для веб-интерфейса, используем порог по умолчанию: %d", perPage)
	}

	// Вычисляем смещение
	offset := (page - 1) * perPage

	// Определяем, авторизован ли пользователь для фильтрации по over18
	isAuthenticated := w.isAuthenticated(r)

	// Очищаем и подготавливаем поисковый запрос
	cleanQuery := strings.TrimSpace(queryStr)

	if queryStr != "" && cleanQuery != "" {
		searchPattern := "%" + cleanQuery + "%"

		// Добавляем логирование для отладки
		//		log.Printf("Search query: '%s', pattern: '%s'", cleanQuery, searchPattern)

		// Формируем базовую часть WHERE для поиска (оптимизировано для SQLite)
		searchCondition := `
		(b.title LIKE ? COLLATE ICU_NOCASE OR
		 IFNULL(b.series, '') LIKE ? COLLATE ICU_NOCASE OR
		 EXISTS (
			SELECT 1 FROM book_authors ba
			JOIN authors a ON ba.author_id = a.id
			WHERE ba.book_id = b.id AND a.full_name LIKE ? COLLATE ICU_NOCASE
		 ) OR
		 EXISTS (
			SELECT 1 FROM book_tags bt
			JOIN tags t ON bt.tag_id = t.id
			WHERE bt.book_id = b.id AND t.name LIKE ? COLLATE ICU_NOCASE
		 ))
	`

		// Формируем полный WHERE и аргументы в зависимости от авторизации
		var fullWhereCondition string
		var argsCount []interface{}
		var argsSelect []interface{}

		if isAuthenticated {
			// Для авторизованных: только условие поиска
			fullWhereCondition = "WHERE " + searchCondition
			argsCount = []interface{}{searchPattern, searchPattern, searchPattern, searchPattern}
			argsSelect = []interface{}{searchPattern, searchPattern, searchPattern, searchPattern}
		} else {
			// Для неавторизованных: условие поиска И фильтр по over18
			fullWhereCondition = "WHERE (" + searchCondition + ") AND IFNULL(b.over18, 0) = 0"
			argsCount = []interface{}{searchPattern, searchPattern, searchPattern, searchPattern}
			argsSelect = []interface{}{searchPattern, searchPattern, searchPattern, searchPattern}
		}

		// Запрос для подсчета общего количества найденных книг
		countQuery := fmt.Sprintf(`
		SELECT COUNT(*)
		FROM books b
		%s
	`, fullWhereCondition)

		err = w.db.QueryRow(countQuery, argsCount...).Scan(&totalBooks)
		if err != nil {
			log.Printf("Database error getting total books count with query '%s': %v", queryStr, err)
			log.Printf("Count query: %s", countQuery)
			log.Printf("Count args: %v", argsCount)
			http.Error(wr, "Database error", http.StatusInternalServerError)
			return
		}

		//		log.Printf("Found %d books for query '%s'", totalBooks, cleanQuery)

		// Дополнительная диагностика - проверим отдельно каждое условие поиска
		if totalBooks == 0 {
			var titleCount, seriesCount, authorCount, tagCount int

			// Проверка поиска по названию
			w.db.QueryRow("SELECT COUNT(*) FROM books WHERE title LIKE ? COLLATE ICU_NOCASE", searchPattern).Scan(&titleCount)
			//			log.Printf("Title matches: %d", titleCount)

			// Проверка поиска по серии
			w.db.QueryRow("SELECT COUNT(*) FROM books WHERE IFNULL(series, '') LIKE ? COLLATE ICU_NOCASE", searchPattern).Scan(&seriesCount)
			//			log.Printf("Series matches: %d", seriesCount)

			// Проверка поиска по авторам
			w.db.QueryRow(`SELECT COUNT(DISTINCT b.id) FROM books b
			JOIN book_authors ba ON ba.book_id = b.id
			JOIN authors a ON ba.author_id = a.id
			WHERE a.full_name LIKE ? COLLATE ICU_NOCASE`, searchPattern).Scan(&authorCount)
			//			log.Printf("Author matches: %d", authorCount)

			// Проверка поиска по тегам
			w.db.QueryRow(`SELECT COUNT(DISTINCT b.id) FROM books b
			JOIN book_tags bt ON bt.book_id = b.id
			JOIN tags t ON bt.tag_id = t.id
			WHERE t.name LIKE ? COLLATE ICU_NOCASE`, searchPattern).Scan(&tagCount)
			// log.Printf("Tag matches: %d", tagCount)

		}

		// Запрос для выборки найденных книг
		selectQuery := fmt.Sprintf(`
		SELECT b.id, b.title, b.file_type, b.file_hash, b.over18,
			(SELECT CASE
				WHEN COUNT(*) > 2 THEN 'коллектив авторов'
				WHEN COUNT(*) = 0 THEN 'Автор не указан'
				ELSE GROUP_CONCAT(a.full_name, ', ')
			END
			FROM book_authors ba
			LEFT JOIN authors a ON ba.author_id = a.id
			WHERE ba.book_id = b.id) as authors_str,
			(SELECT GROUP_CONCAT(t.name, ', ')
			FROM book_tags bt
			LEFT JOIN tags t ON bt.tag_id = t.id
			WHERE bt.book_id = b.id) as tags_str
		FROM books b
		%s
		GROUP BY b.id, b.title, b.file_type, b.file_hash, b.over18
		ORDER BY b.id DESC
		LIMIT ? OFFSET ?
	`, fullWhereCondition)

		// Добавляем аргументы пагинации в конец списка аргументов для выборки
		argsSelect = append(argsSelect, perPage, offset)

		rows, err = w.db.Query(selectQuery, argsSelect...)
		if err != nil {
			log.Printf("Database error getting books with query '%s': %v", queryStr, err)
			log.Printf("Select query: %s", selectQuery)
			log.Printf("Select args: %v", argsSelect)
			http.Error(wr, "Database error", http.StatusInternalServerError)
			return
		}

	} else {
		// --- ЛОГИКА БЕЗ ПОИСКА ---
		if isAuthenticated {
			// Авторизованные пользователи видят все книги
			err = w.db.QueryRow("SELECT COUNT(*) FROM books").Scan(&totalBooks)
			if err != nil {
				log.Printf("Database error getting total books count: %v", err)
				http.Error(wr, "Database error", http.StatusInternalServerError)
				return
			}

			rows, err = w.db.Query(`
			SELECT b.id, b.title, b.file_type, b.file_hash, b.over18,
				(SELECT CASE
					WHEN COUNT(*) > 2 THEN 'коллектив авторов'
					WHEN COUNT(*) = 0 THEN 'Автор не указан'
					ELSE GROUP_CONCAT(a.full_name, ', ')
				END
				FROM book_authors ba
				LEFT JOIN authors a ON ba.author_id = a.id
				WHERE ba.book_id = b.id) as authors_str,
				(SELECT GROUP_CONCAT(t.name, ', ')
				FROM book_tags bt
				LEFT JOIN tags t ON bt.tag_id = t.id
				WHERE bt.book_id = b.id) as tags_str
			FROM books b
			GROUP BY b.id, b.title, b.file_type, b.file_hash, b.over18
			ORDER BY b.id DESC
			LIMIT ? OFFSET ?`, perPage, offset)
			if err != nil {
				log.Printf("Database error getting books: %v", err)
				http.Error(wr, "Database error", http.StatusInternalServerError)
				return
			}
		} else {
			// Неавторизованные пользователи не видят книги 18+
			err = w.db.QueryRow("SELECT COUNT(*) FROM books WHERE over18 = 0 OR over18 IS NULL").Scan(&totalBooks)
			if err != nil {
				log.Printf("Database error getting total books count (non-auth): %v", err)
				http.Error(wr, "Database error", http.StatusInternalServerError)
				return
			}

			rows, err = w.db.Query(`
			SELECT b.id, b.title, b.file_type, b.file_hash, b.over18,
				(SELECT CASE
					WHEN COUNT(*) > 2 THEN 'коллектив авторов'
					WHEN COUNT(*) = 0 THEN 'Автор не указан'
					ELSE GROUP_CONCAT(a.full_name, ', ')
				END
				FROM book_authors ba
				LEFT JOIN authors a ON ba.author_id = a.id
				WHERE ba.book_id = b.id) as authors_str,
				(SELECT GROUP_CONCAT(t.name, ', ')
				FROM book_tags bt
				LEFT JOIN tags t ON bt.tag_id = t.id
				WHERE bt.book_id = b.id) as tags_str
			FROM books b
			WHERE b.over18 = 0 OR b.over18 IS NULL
			GROUP BY b.id, b.title, b.file_type, b.file_hash, b.over18
			ORDER BY b.id DESC
			LIMIT ? OFFSET ?`, perPage, offset)
			if err != nil {
				log.Printf("Database error getting books (non-auth): %v", err)
				http.Error(wr, "Database error", http.StatusInternalServerError)
				return
			}
		}
	}

	defer rows.Close()

	var books []models.BookWeb
	for rows.Next() {
		var b models.BookWeb

		var fileType, fileHash, tagsStr sql.NullString
		var authorsStr string
		var over18 sql.NullBool

		err := rows.Scan(&b.ID, &b.Title, &fileType, &fileHash, &over18, &authorsStr, &tagsStr)
		if err != nil {
			log.Printf("Error scanning book row: %v", err)
			continue
		}

		// Обработка полученных данных:
		if fileType.Valid {
			b.FileType = fileType.String
		}
		if fileHash.Valid {
			b.FileHash = fileHash.String
			// Получаем путь к обложке по хешу
			b.CoverURL = w.getCoverURLFromFileHash(fileHash.String)
		} else {
			b.CoverURL = ""
		}
		if over18.Valid {
			b.Over18 = over18.Bool
		}
		if tagsStr.Valid {
			b.TagsStr = tagsStr.String
		}
		if authorsStr != "" {
			b.AuthorsStr = authorsStr
		} else {
			b.AuthorsStr = "Автор не указан"
		}
		books = append(books, b)
	}
	// Проверяем ошибку после итерации
	if err = rows.Err(); err != nil {
		log.Printf("Error iterating book rows: %v", err)
		http.Error(wr, "Database error", http.StatusInternalServerError)
		return
	}

	// Вычисляем общее количество страниц
	totalPages := (totalBooks + perPage - 1) / perPage // Округление вверх
	if totalPages == 0 {
		totalPages = 1 // Хотя бы одна страница должна быть
	}

	// Определяем диапазон страниц для отображения в пагинации (например, 5 страниц вокруг текущей)
	pageRange := 5
	startPage := page - pageRange/2
	if startPage < 1 {
		startPage = 1
	}
	endPage := startPage + pageRange - 1
	if endPage > totalPages {
		endPage = totalPages
		startPage = endPage - pageRange + 1
		if startPage < 1 {
			startPage = 1
		}
	}

	// Подготавливаем данные для шаблона, включая данные пагинации
	data := struct {
		Books           []models.BookWeb
		CurrentPage     int
		TotalPages      int
		StartPage       int
		EndPage         int
		PageNumbers     []int
		PrevPage        int
		NextPage        int
		IsAuthenticated bool
		CatalogTitle    string
		Query           string
		AppTitle        string
		RevisionSuccess bool
		IPFSEnabled     bool
		NostrEnabled    bool
	}{
		Books:           books,
		CurrentPage:     page,
		TotalPages:      totalPages,
		StartPage:       startPage,
		EndPage:         endPage,
		PrevPage:        page - 1,
		NextPage:        page + 1,
		IsAuthenticated: w.isAuthenticated(r),
		CatalogTitle:    w.config.GetCatalogTitle(),
		Query:           queryStr,
		AppTitle:        w.appTitle,
		RevisionSuccess: revisionSuccess,
		IPFSEnabled:     w.isIPFSAvailable(),
		NostrEnabled:    w.isNostrAvailable(),
	}

	// Генерируем список номеров страниц для отображения
	for i := startPage; i <= endPage; i++ {
		data.PageNumbers = append(data.PageNumbers, i)
	}

	// Загружаем шаблон из файла
	tmplPath := filepath.Join(w.rootPath, "web", "templates", "catalog.html")
	tmpl, err := template.New("catalog.html").Funcs(template.FuncMap{
		"sub":        func(a, b int) int { return a - b },
		"split":      strings.Split,
		"trim":       strings.TrimSpace,
		"urlquery":   url.QueryEscape,
		"formatSize": FormatFileSize,
	}).ParseFiles(tmplPath)

	if err != nil {
		log.Printf("Error parsing template: %v", err)
		http.Error(wr, "Template error", http.StatusInternalServerError)
		return
	}

	// Выполняем шаблон и записываем результат
	wr.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(wr, "catalog", data); err != nil {
		log.Printf("Error executing template: %v", err)
		http.Error(wr, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	fmt.Printf("Отображено %d книг на странице %d из %d (Поиск: '%s')\n", len(books), page, totalPages, queryStr)
}

// RevisionHandler обрабатывает запрос на полную ревизию библиотеки
func (w *WebInterface) RevisionHandler(wr http.ResponseWriter, r *http.Request) {
	cfg := config.GetConfig()

	if cfg.Debug {
		log.Println("RevisionHandler: Начало обработки запроса")
	}

	// Проверяем аутентификацию
	if !w.isAuthenticated(r) {
		log.Println("RevisionHandler: Пользователь не аутентифицирован")
		http.Error(wr, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if r.Method != http.MethodPost {
		log.Printf("RevisionHandler: Неверный метод %s", r.Method)
		http.Error(wr, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if cfg.Debug {
		log.Println("RevisionHandler: Пользователь аутентифицирован, метод POST")
	}

	// Передаем DB и конфигурацию в scanner
	scanner.SetDB(w.db)
	if w.config != nil {
		scanner.SetConfig(w.config)
		if cfg.Debug {
			log.Println("RevisionHandler: Конфигурация и БД переданы в scanner")
		}
	} else {
		if cfg.Debug {
			log.Println("RevisionHandler: Предупреждение - конфигурация не установлена")
		}
	}

	// Критическая проверка доступа к БД
	var dummy int
	err := w.db.QueryRow("SELECT 1").Scan(&dummy)
	if err != nil {
		log.Printf("RevisionHandler: Критическая ошибка доступа к БД: %v", err)
		http.Error(wr, "Критическая ошибка: невозможно получить доступ к базе данных", http.StatusInternalServerError)
		return
	}
	//	log.Println("RevisionHandler: Доступ к БД подтвержден")

	// Сбрасываем прогресс перед началом
	ResetRevisionProgress()

	// Отправляем ответ клиенту немедленно
	wr.Header().Set("Content-Type", "application/json")
	wr.WriteHeader(http.StatusOK)
	json.NewEncoder(wr).Encode(map[string]interface{}{
		"success": true,
		"message": "Ревизия начата. Пожалуйста, подождите завершения операции.",
		"status":  "started",
	})

	if cfg.Debug {
		log.Println("RevisionHandler: Отправлен ответ 200 OK клиенту")
	}

	// --- АСИНХРОННОЕ ВЫПОЛНЕНИЕ РЕВИЗИИ ---
	go func() {
		logPrefix := "[Асинхронная ревизия]"
		if cfg.Debug {
			log.Printf("%s Начинаем полную ревизию библиотеки в фоновом режиме...", logPrefix)
		}

		// Определяем операции с весами для прогресса
		operations := []struct {
			name   string
			fn     func() error
			weight int // вес операции в процентах
		}{
			{"Сканирование каталога книг", scanner.ScanBooksDirectory, 25},
			{"Очистка отсутствующих файлов", scanner.CleanupMissingFiles, 10},
			{"Очистка неиспользуемых данных", scanner.CleanupOrphanedData, 10},
			{"Очистка данных Nostr", w.cleanupAllNostrData, 10},
			{"Переименование книг по конфигурации", scanner.RenameBooksAccordingToConfig, 10},
			{"Создание недостающих обложек", scanner.GenerateMissingCovers, 15},
			{"Создание недостающих аннотаций", scanner.GenerateMissingAnnotations, 10},
			{"Добавление недостающих ссылок IPFS", w.addMissingIPFSLinks, 10},
		}

		totalWeight := 0
		for _, op := range operations {
			totalWeight += op.weight
		}

		currentProgress := 0
		SetRevisionProgress(currentProgress, "Начало ревизии...")

		for i, op := range operations {
			if cfg.Debug {
				log.Printf("%s Выполняем: %s (%d/%d)", logPrefix, op.name, i+1, len(operations))
			}
			SetRevisionProgress(currentProgress, fmt.Sprintf("Выполняем: %s", op.name))

			err := op.fn()
			if err != nil {
				if cfg.Debug {
					log.Printf("%s Ошибка при выполнении '%s': %v", logPrefix, op.name, err)
				}
				SetRevisionProgress(currentProgress, fmt.Sprintf("Ошибка: %s - %v", op.name, err))
				// Продолжаем выполнение других шагов
			} else {
				if cfg.Debug {
					log.Printf("%s Успешно завершено: %s", logPrefix, op.name)
				}
			}

			// Обновляем прогресс
			currentProgress += op.weight
			if currentProgress > 100 {
				currentProgress = 100
			}
			SetRevisionProgress(currentProgress, fmt.Sprintf("Завершено: %s", op.name))
		}

		SetRevisionCompleted()
		log.Printf("%s Полная ревизия завершена!", logPrefix)
	}()
}

// ProgressHandler возвращает текущий прогресс ревизии
func (w *WebInterface) ProgressHandler(wr http.ResponseWriter, r *http.Request) {
	wr.Header().Set("Content-Type", "application/json")
	progress := GetRevisionProgress()
	json.NewEncoder(wr).Encode(progress)
}

// addMissingIPFSLinks добавляет недостающие ссылки IPFS для книг, у которых их нет
func (w *WebInterface) addMissingIPFSLinks() error {
	cfg := config.GetConfig()

	if cfg.Debug {
		log.Println("addMissingIPFSLinks: Начало функции")
	}

	if w.db == nil {
		return fmt.Errorf("база данных не инициализирована")
	}

	// Проверяем, включена ли поддержка IPFS
	if w.config == nil || w.config.LocalIPFSAPI == "" {
		if cfg.Debug {
			log.Println("Поддержка IPFS не включена в конфигурации")
		}
		return nil
	}

	if cfg.Debug {
		log.Println("Начинаю добавление недостающих ссылок IPFS...")
	}

	dbConn := w.db

	// Получаем все книги без IPFS CID
	if cfg.Debug {
		log.Println("addMissingIPFSLinks: Выполняю SQL-запрос...")
	}
	query := `
		SELECT id, file_url, file_hash
		FROM books
		WHERE (ipfs_cid IS NULL OR ipfs_cid = '')
		AND file_hash IS NOT NULL AND file_hash != ''
		AND file_url IS NOT NULL AND file_url != ''
		ORDER BY id
	`
	rows, err := dbConn.Query(query)
	if err != nil {
		return fmt.Errorf("ошибка получения списка книг без IPFS CID: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			if cfg.Debug {
				log.Printf("Ошибка закрытия rows: %v", closeErr)
			}
		} else {
			if cfg.Debug {
				log.Println("addMissingIPFSLinks: rows закрыты")
			}
		}
	}()

	if cfg.Debug {
		log.Println("addMissingIPFSLinks: SQL-запрос выполнен успешно")
	}

	addedCount := 0
	errorCount := 0
	processedCount := 0

	for rows.Next() {
		processedCount++
		if cfg.Debug {
			log.Printf("addMissingIPFSLinks: Обрабатываю книгу #%d", processedCount)
		}

		var bookID int
		var fileURL, fileHash string
		err := rows.Scan(&bookID, &fileURL, &fileHash)
		if err != nil {
			if cfg.Debug {
				log.Printf("Ошибка сканирования строки книги: %v", err)
			}
			errorCount++
			continue
		}
		if cfg.Debug {
			log.Printf("addMissingIPFSLinks: Книга ID=%d, URL=%s, Hash=%s", bookID, fileURL, fileHash)
		}

		// Преобразуем URL в путь файловой системы
		relPath := strings.TrimPrefix(fileURL, "/")
		var filePath string
		if w.rootPath != "" {
			// Если задан rootPath, формируем абсолютный путь
			filePath = filepath.Join(w.rootPath, filepath.FromSlash(relPath))
		} else {
			// Если не задан, используем старую логику (для обратной совместимости)
			filePath = filepath.FromSlash(relPath)
		}

		// Проверяем существование файла
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			if cfg.Debug {
				log.Printf("Файл не найден для книги ID %d: %s", bookID, filePath)
			}
			errorCount++
			continue
		} else if err != nil {
			if cfg.Debug {
				log.Printf("Ошибка проверки файла %s для книги ID %d: %v", filePath, bookID, err)
			}
			errorCount++
			continue
		}

		// Получаем информацию о файле
		fileInfo, err := os.Stat(filePath)
		if err != nil {
			if cfg.Debug {
				log.Printf("Ошибка получения информации о файле %s для книги ID %d: %v", filePath, bookID, err)
			}
			errorCount++
			continue
		}

		// Добавляем файл в IPFS используя существующую логику
		if cfg.Debug {
			log.Printf("addMissingIPFSLinks: Начинаю загрузку в IPFS файла %s...", filePath)
		}
		ipfsCID, err := w.addFileToIPFS(filePath, fileInfo)
		if err != nil {
			if cfg.Debug {
				log.Printf("Ошибка загрузки файла %s в IPFS для книги ID %d: %v", filePath, bookID, err)
			}
			errorCount++
			continue // Продолжаем со следующей книгой
		}
		if cfg.Debug {
			log.Printf("addMissingIPFSLinks: Загрузка в IPFS завершена. CID=%s", ipfsCID)
		}

		if ipfsCID != "" {
			// --- Используем основное соединение dbConn ---
			// Обновляем запись в БД с повторными попытками при блокировке
			if cfg.Debug {
				log.Printf("addMissingIPFSLinks: Обновляю БД для книги ID=%d с CID=%s", bookID, ipfsCID)
			}
			// Убираем updateIPFSCIDWithRetry, так как используем то же соединение
			_, err = dbConn.Exec("UPDATE books SET ipfs_cid = ? WHERE id = ?", ipfsCID, bookID)
			if err != nil {
				if cfg.Debug {
					log.Printf("Ошибка обновления IPFS CID для книги ID %d: %v", bookID, err)
				}
				errorCount++
				continue // Продолжаем со следующей книгой
			}
			if cfg.Debug {
				log.Printf("addMissingIPFSLinks: БД успешно обновлена для книги ID=%d", bookID)
			}

			if cfg.Debug {
				log.Printf("Добавлена ссылка IPFS для книги ID %d: %s -> %s", bookID, filePath, ipfsCID)
			}
			addedCount++
		} else {
			if cfg.Debug {
				log.Printf("CID не был получен для файла %s (книга ID %d), пропускаю обновление БД.", filePath, bookID)
			}
		}

		// Небольшая задержка между обработкой книг, чтобы не перегружать IPFS и БД
		//		log.Printf("addMissingIPFSLinks: Задержка перед следующей итерацией...")
		time.Sleep(200 * time.Millisecond)
	}

	if err = rows.Err(); err != nil {
		return fmt.Errorf("ошибка итерации по результатам запроса: %w", err)
	}

	log.Printf("Добавление ссылок IPFS завершено. Обработано: %d, Добавлено: %d, Ошибок: %d", processedCount, addedCount, errorCount)

	// Возвращаем ошибку, если все попытки завершились неудачей (например, проблемы с БД),
	// но позволяем частичный успех.
	if processedCount > 0 && addedCount == 0 && errorCount > 0 {
		return fmt.Errorf("не удалось добавить ни одной ссылки IPFS из %d попыток", errorCount)
	}
	return nil
}

// updateIPFSCIDWithRetry обновляет IPFS CID в БД с повторными попытками при блокировке
func (w *WebInterface) updateIPFSCIDWithRetry(db *sql.DB, bookID int, ipfsCID string, maxRetries int) error {
	cfg := config.GetConfig()

	for i := 0; i < maxRetries; i++ {
		_, err := db.Exec("UPDATE books SET ipfs_cid = ? WHERE id = ?", ipfsCID, bookID)
		if err == nil {
			return nil // Успех
		}

		// Проверяем, является ли ошибка блокировкой БД
		if strings.Contains(err.Error(), "database is locked") || strings.Contains(err.Error(), "database locked") {
			if cfg.Debug {
				log.Printf("База данных заблокирована, попытка %d/%d для книги ID %d", i+1, maxRetries, bookID)
			}
			time.Sleep(time.Duration(i+1) * 200 * time.Millisecond) // Увеличивающаяся задержка
			continue
		}

		// Другая ошибка - возвращаем немедленно
		return err
	}

	return fmt.Errorf("не удалось обновить IPFS CID после %d попыток: database is locked", maxRetries)
}

// Для ревизии библиотеки
type RevisionProgress struct {
	mu       sync.RWMutex
	Status   string    `json:"status"`   // "running", "completed", "error"
	Progress int       `json:"progress"` // 0-100
	Message  string    `json:"message"`  // текущее сообщение
	Error    string    `json:"error"`    // ошибка, если есть
	Started  time.Time `json:"started"`  // время начала
}

var revisionProgress = &RevisionProgress{
	Status:   "idle",
	Progress: 0,
	Message:  "Ожидание начала ревизии",
}

// Функции для обновления прогресса
func SetRevisionProgress(progress int, message string) {
	revisionProgress.mu.Lock()
	defer revisionProgress.mu.Unlock()
	revisionProgress.Progress = progress
	revisionProgress.Message = message
	if revisionProgress.Status != "error" {
		revisionProgress.Status = "running"
	}
}

func SetRevisionError(err error) {
	revisionProgress.mu.Lock()
	defer revisionProgress.mu.Unlock()
	revisionProgress.Status = "error"
	revisionProgress.Error = err.Error()
}

func SetRevisionCompleted() {
	revisionProgress.mu.Lock()
	defer revisionProgress.mu.Unlock()
	revisionProgress.Status = "completed"
	revisionProgress.Progress = 100
	revisionProgress.Message = "Ревизия завершена"
}

func ResetRevisionProgress() {
	revisionProgress.mu.Lock()
	defer revisionProgress.mu.Unlock()
	revisionProgress.Status = "running"
	revisionProgress.Progress = 0
	revisionProgress.Message = "Начало ревизии"
	revisionProgress.Error = ""
	revisionProgress.Started = time.Now()
}

func GetRevisionProgress() *RevisionProgress {
	revisionProgress.mu.RLock()
	defer revisionProgress.mu.RUnlock()
	// Создаем копию для безопасности
	return &RevisionProgress{
		Status:   revisionProgress.Status,
		Progress: revisionProgress.Progress,
		Message:  revisionProgress.Message,
		Error:    revisionProgress.Error,
		Started:  revisionProgress.Started,
	}
}
