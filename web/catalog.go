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
func (w *WebInterface) ShowWebInterface(wr http.ResponseWriter, r *http.Request, cfg *config.Config) {

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
		// Используем lower-версию поискового запроса для поиска
		lowerQuery := strings.ToLower(cleanQuery)

		// Добавляем логирование для отладки
		//		log.Printf("Search query: '%s', lower query: '%s'", cleanQuery, lowerQuery)

		// Формируем базовую часть WHERE для поиска с использованием lower-полей
		searchCondition := `
		(b.title_lower LIKE ? OR
		 IFNULL(b.series_lower, '') LIKE ? OR
		 EXISTS (
			SELECT 1 FROM book_authors ba
			JOIN authors a ON ba.author_id = a.id
			WHERE ba.book_id = b.id AND a.full_name_lower LIKE ?
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
			argsCount = []interface{}{"%" + lowerQuery + "%", "%" + lowerQuery + "%", "%" + lowerQuery + "%", "%" + cleanQuery + "%"}
			argsSelect = []interface{}{"%" + lowerQuery + "%", "%" + lowerQuery + "%", "%" + lowerQuery + "%", "%" + cleanQuery + "%"}
		} else {
			// Для неавторизованных: условие поиска И фильтр по over18
			fullWhereCondition = "WHERE (" + searchCondition + ") AND IFNULL(b.over18, 0) = 0"
			argsCount = []interface{}{"%" + lowerQuery + "%", "%" + lowerQuery + "%", "%" + lowerQuery + "%", "%" + cleanQuery + "%"}
			argsSelect = []interface{}{"%" + lowerQuery + "%", "%" + lowerQuery + "%", "%" + lowerQuery + "%", "%" + cleanQuery + "%"}
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
			searchPatternLower := "%" + lowerQuery + "%"
			searchPatternOriginal := "%" + cleanQuery + "%"

			// Проверка поиска по названию
			w.db.QueryRow("SELECT COUNT(*) FROM books WHERE title_lower LIKE ?", searchPatternLower).Scan(&titleCount)
			//			log.Printf("Title matches: %d", titleCount)

			// Проверка поиска по серии
			w.db.QueryRow("SELECT COUNT(*) FROM books WHERE IFNULL(series_lower, '') LIKE ?", searchPatternLower).Scan(&seriesCount)
			//			log.Printf("Series matches: %d", seriesCount)

			// Проверка поиска по авторам
			w.db.QueryRow(`SELECT COUNT(DISTINCT b.id) FROM books b
			JOIN book_authors ba ON ba.book_id = b.id
			JOIN authors a ON ba.author_id = a.id
			WHERE a.full_name_lower LIKE ?`, searchPatternLower).Scan(&authorCount)
			//			log.Printf("Author matches: %d", authorCount)

			// Проверка поиска по тегам (оставляем COLLATE ICU_NOCASE, т.к. теги не имеют lower-полей)
			w.db.QueryRow(`SELECT COUNT(DISTINCT b.id) FROM books b
			JOIN book_tags bt ON bt.book_id = b.id
			JOIN tags t ON bt.tag_id = t.id
			WHERE t.name LIKE ? COLLATE ICU_NOCASE`, searchPatternOriginal).Scan(&tagCount)
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
			b.CoverURL = w.getCoverURLFromFileHash(fileHash.String, w.config)
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
	if cfg.Debug {
		log.Printf("Отображено %d книг на странице %d из %d (Поиск: '%s')\n", len(books), page, totalPages, queryStr)
	}
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
		log.Printf("%s Начинаем полную ревизию библиотеки в фоновом режиме...", logPrefix)

		// Определяем операции с весами для прогресса
		operations := []struct {
			name   string
			fn     func() error
			weight int // вес операции в процентах
		}{
			{"Заполнение недостающих полей поиска", scanner.FillMissingLowercaseFields, 2},   // 1. Сначала заполняем пустые поля
			{"Очистка отсутствующих файлов", scanner.CleanupMissingFiles, 2},                 // 2. Удаляем записи для *отсутствующих* файлов из БД
			{"Сканирование каталога книг", scanner.ScanBooksDirectory, 76},                   // 3. Находим *новые* файлы, добавляем в БД
			{"Переименование книг по конфигурации", scanner.RenameBooksAccordingToConfig, 2}, // 4. Переименовываем файлы *и обновляем БД*
			{"Очистка неиспользуемых данных", scanner.CleanupOrphanedData, 2},                // 5. Удаляем неиспользуемых авторов/тегов (после переименования)
			{"Очистка данных nostr", w.cleanupAllNostrData, 2},                               // 6. Очистка Nostr
			{"Создание недостающих обложек", scanner.GenerateMissingCovers, 5},               // 7. Создаём обложки
			{"Создание недостающих аннотаций", scanner.GenerateMissingAnnotations, 2},        // 8. Создаём аннотации
			{"Добавление недостающих ссылок IPFS", w.addMissingIPFSLinks, 5},                 // 9. Добавляем IPFS
			{"Очистка лишних файлов в каталоге", scanner.CleanupExtraFiles, 2},               // 10. Удаляем файлы, не связанные с БД
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

	// --- Шаг 1: Получить все книги без IPFS CID ---
	// Делаем это в отдельной области видимости, чтобы rows.Close() был вызван как можно раньше.
	type bookForIPFS struct {
		id       int
		fileURL  string // Абсолютный путь к файлу
		fileHash string
	}
	var booksToProcess []bookForIPFS

	if cfg.Debug {
		log.Println("addMissingIPFSLinks: Выполняю SQL-запрос для получения списка книг...")
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
	// defer rows.Close() гарантирует, что rows будет закрыт при выходе из функции,
	// даже если будет ошибка или return раньше.
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

	// Читаем все данные из rows как можно быстрее и закрываем rows.
	for rows.Next() {
		var b bookForIPFS
		err := rows.Scan(&b.id, &b.fileURL, &b.fileHash)
		if err != nil {
			if cfg.Debug {
				log.Printf("Ошибка сканирования строки книги: %v", err)
			}
			// Продолжаем со следующей строкой, не останавливая весь процесс
			continue
		}
		booksToProcess = append(booksToProcess, b)
	}

	// Проверяем ошибки после итерации
	if err = rows.Err(); err != nil {
		return fmt.Errorf("ошибка итерации по результатам запроса: %w", err)
	}
	// rows.Close() будет вызван здесь неявно через defer

	if cfg.Debug {
		log.Printf("addMissingIPFSLinks: SQL-запрос выполнен успешно. Найдено %d книг для обработки.", len(booksToProcess))
	}

	// --- Шаг 2: Обрабатываем каждую книгу ---
	addedCount := 0
	errorCount := 0
	processedCount := 0

	for _, book := range booksToProcess {
		processedCount++
		if cfg.Debug {
			log.Printf("addMissingIPFSLinks: Обрабатываю книгу #%d (ID: %d)", processedCount, book.id)
		}

		if cfg.Debug {
			log.Printf("addMissingIPFSLinks: Книга ID=%d, URL=%s, Hash=%s", book.id, book.fileURL, book.fileHash)
		}

		// Используем fileURL напрямую как путь к файлу
		// fileURL теперь хранит абсолютный путь к файлу
		filePath := book.fileURL

		// Проверяем существование файла
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			if cfg.Debug {
				log.Printf("Файл не найден для книги ID %d: %s", book.id, filePath)
			}
			errorCount++
			continue
		} else if err != nil {
			if cfg.Debug {
				log.Printf("Ошибка проверки файла %s для книги ID %d: %v", filePath, book.id, err)
			}
			errorCount++
			continue
		}

		// Получаем информацию о файле
		fileInfo, err := os.Stat(filePath)
		if err != nil {
			if cfg.Debug {
				log.Printf("Ошибка получения информации о файле %s для книги ID %d: %v", filePath, book.id, err)
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
				log.Printf("Ошибка загрузки файла %s в IPFS для книги ID %d: %v", filePath, book.id, err)
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
				log.Printf("addMissingIPFSLinks: Обновляю БД для книги ID=%d с CID=%s", book.id, ipfsCID)
			}
			// ИСПОЛЬЗУЕМ updateIPFSCIDWithRetry
			err = updateIPFSCIDWithRetry(dbConn, book.id, ipfsCID, 5) // 5 попыток
			if err != nil {
				if cfg.Debug {
					log.Printf("Ошибка обновления IPFS CID для книги ID %d: %v", book.id, err)
				}
				errorCount++
				continue // Продолжаем со следующей книгой
			}
			if cfg.Debug {
				log.Printf("addMissingIPFSLinks: БД успешно обновлена для книги ID=%d", book.id)
			}

			if cfg.Debug {
				log.Printf("Добавлена ссылка IPFS для книги ID %d: %s -> %s", book.id, filePath, ipfsCID)
			}
			addedCount++
		} else {
			if cfg.Debug {
				log.Printf("CID не был получен для файла %s (книга ID %d), пропускаю обновление БД.", filePath, book.id)
			}
		}

		// Небольшая задержка между обработкой книг, чтобы не перегружать IPFS и БД
		// log.Printf("addMissingIPFSLinks: Задержка перед следующей итерацией...")
		time.Sleep(200 * time.Millisecond)
	}

	log.Printf("Добавление ссылок IPFS завершено. Обработано: %d, Добавлено: %d, Ошибок: %d", processedCount, addedCount, errorCount)

	// Возвращаем ошибку, если все попытки завершились неудачей (например, проблемы с БД),
	// но позволяем частичный успех.
	// if processedCount > 0 && addedCount == 0 && errorCount > 0 {
	// 	return fmt.Errorf("не удалось добавить ни одной ссылки IPFS из %d попыток", errorCount)
	// }
	// Лучше не возвращать ошибку, если были частичные успехи, чтобы не останавливать всю ревизию.
	return nil
}

// updateIPFSCIDWithRetry обновляет IPFS CID в БД с повторными попытками при блокировке
func updateIPFSCIDWithRetry(database *sql.DB, bookID int, ipfsCID string, maxRetries int) error {
	cfg := config.GetConfig()

	var lastErr error // Переменная для хранения последней ошибки

	for i := 0; i < maxRetries; i++ {
		_, err := database.Exec("UPDATE books SET ipfs_cid = ? WHERE id = ?", ipfsCID, bookID)
		if err == nil {
			return nil // Успех
		}

		// Сохраняем последнюю ошибку
		lastErr = err

		// Проверяем, является ли ошибка блокировкой БД
		if strings.Contains(err.Error(), "database is locked") || strings.Contains(err.Error(), "database locked") {
			if cfg.Debug {
				log.Printf("База данных заблокирована, попытка %d/%d для книги ID %d: %v", i+1, maxRetries, bookID, err)
			}
			// Увеличивающаяся задержка перед повторной попыткой
			time.Sleep(time.Duration(i+1) * 200 * time.Millisecond)
			continue
		}

		// Другая ошибка - возвращаем немедленно
		return err
	}

	// Если цикл завершился без успеха, возвращаем последнюю ошибку
	return fmt.Errorf("не удалось обновить IPFS CID после %d попыток: %v", maxRetries, lastErr)
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
