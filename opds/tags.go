package opds

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"turanga/config"
	"turanga/models"
)

// TagHandler отвечает за обработку запросов к /tags
type TagHandler struct {
	*BaseHandler
}

// NewTagHandler создает новый экземпляр TagHandler
func NewTagHandler(database *sql.DB, cfg *config.Config) *TagHandler {
	return &TagHandler{
		BaseHandler: NewBaseHandler(database, cfg),
	}
}

// TagsHandler обрабатывает запрос к /tags
func (th *TagHandler) TagsHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/tags" && r.URL.Path != "/tags/" {
		tagPath := strings.TrimPrefix(r.URL.Path, "/tags/")
		tagName, err := url.QueryUnescape(tagPath)
		if err != nil {
			tagName = tagPath
		}
		tagName = strings.TrimPrefix(tagName, "/")

		if tagName != "" {
			th.showBooksForTag(w, r, tagName)
			return
		}
	}

	// Подсчитываем теги
	tagCount, err := th.CountItems(`
        SELECT COUNT(*)
        FROM tags
    `)
	if err != nil {
		log.Printf("Ошибка подсчета тегов: %v", err)
		http.Error(w, "Database error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Если тегов больше порога, показываем группировку по буквам
	if th.ShouldPaginate(tagCount) {
		th.showTagsLetters(w, r)
		return
	}

	th.showAllTags(w, r)
}

// showTagsLetters показывает каталог с буквами алфавита для тегов
func (th *TagHandler) showTagsLetters(w http.ResponseWriter, r *http.Request) {
	query := `
        SELECT DISTINCT 
            CASE 
                WHEN UPPER(SUBSTR(t.name, 1, 1)) BETWEEN 'A' AND 'Z' THEN UPPER(SUBSTR(t.name, 1, 1))
                WHEN SUBSTR(t.name, 1, 1) BETWEEN 'А' AND 'Я' THEN SUBSTR(t.name, 1, 1)
                WHEN SUBSTR(t.name, 1, 1) = 'Ё' THEN 'Ё'
                ELSE UPPER(SUBSTR(t.name, 1, 1))
            END as first_letter, 
            COUNT(*) as tag_count
        FROM tags t
        GROUP BY 
            CASE 
                WHEN UPPER(SUBSTR(t.name, 1, 1)) BETWEEN 'A' AND 'Z' THEN UPPER(SUBSTR(t.name, 1, 1))
                WHEN SUBSTR(t.name, 1, 1) BETWEEN 'А' AND 'Я' THEN SUBSTR(t.name, 1, 1)
                WHEN SUBSTR(t.name, 1, 1) = 'Ё' THEN 'Ё'
                ELSE UPPER(SUBSTR(t.name, 1, 1))
            END
        ORDER BY 
            CASE 
                WHEN first_letter BETWEEN 'A' AND 'Z' THEN 1
                WHEN first_letter BETWEEN 'А' AND 'Я' THEN 2
                WHEN first_letter = 'Ё' THEN 3
                ELSE 4
            END,
            first_letter
    `

	letterRows, err := th.db.Query(query)
	if err != nil {
		log.Printf("Ошибка запроса букв тегов: %v", err)
		http.Error(w, "Database error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer letterRows.Close()

	entries, err := CreateAlphabetEntries(letterRows, "/tags", "Теги")
	if err != nil {
		log.Printf("Ошибка создания записей букв: %v", err)
		http.Error(w, "Database error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	th.RenderNavigationFeed(w, "Теги по алфавиту", entries)
}

// showTagsForLetter показывает теги на определенную букву
func (th *TagHandler) showTagsForLetter(w http.ResponseWriter, r *http.Request, letter string) {
	if letter == "" {
		http.Error(w, "Invalid letter", http.StatusBadRequest)
		return
	}

	tags, err := th.getTagsByLetter(letter)
	if err != nil {
		log.Printf("Ошибка запроса тегов на букву %s: %v", letter, err)
		http.Error(w, "Database error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	th.renderTagsFeed(w, fmt.Sprintf("Теги на букву \"%s\"", letter), tags)
}

// showAllTags показывает все теги (для случаев, когда их <= 60)
func (th *TagHandler) showAllTags(w http.ResponseWriter, r *http.Request) {
	tags, err := th.getAllTags()
	if err != nil {
		log.Printf("Ошибка запроса тегов: %v", err)
		http.Error(w, "Database error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	th.renderTagsFeed(w, "Все теги", tags)
}

// showBooksForTag показывает книги с определенным тегом
func (th *TagHandler) showBooksForTag(w http.ResponseWriter, r *http.Request, tagName string) {
	if len([]rune(tagName)) == 1 {
		th.showTagsForLetter(w, r, tagName)
		return
	}

	books, err := th.getBooksWithTag(tagName)
	if err != nil {
		log.Printf("Ошибка запроса книг с тегом %s: %v", tagName, err)
		http.Error(w, "Database error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	sortedBooks := th.SortBooksByTitle(books)
	th.RenderAcquisitionFeed(w, fmt.Sprintf("Книги с тегом \"%s\"", tagName), sortedBooks)
}

// getTagsByLetter получает теги на определенную букву
func (th *TagHandler) getTagsByLetter(letter string) ([]*TagInfo, error) {
	var tagRows *sql.Rows
	var err error

	switch strings.ToUpper(letter) {
	case "А", "Б", "В", "Г", "Д", "Е", "Ж", "З", "И", "Й", "К", "Л", "М",
		"Н", "О", "П", "Р", "С", "Т", "У", "Ф", "Х", "Ц", "Ч", "Ш", "Щ", "Ъ", "Ы", "Ь", "Э", "Ю", "Я":
		if strings.ToUpper(letter) == "Ё" {
			tagRows, err = th.db.Query(`
                SELECT t.name, COUNT(bt.book_id) as book_count
                FROM tags t
                LEFT JOIN book_tags bt ON t.id = bt.tag_id
                WHERE (SUBSTR(t.name, 1, 1) = 'Ё' OR SUBSTR(t.name, 1, 1) = 'Е')
                GROUP BY t.name
                ORDER BY t.name
            `)
		} else {
			tagRows, err = th.db.Query(`
                SELECT t.name, COUNT(bt.book_id) as book_count
                FROM tags t
                LEFT JOIN book_tags bt ON t.id = bt.tag_id
                WHERE SUBSTR(t.name, 1, 1) = ?
                GROUP BY t.name
                ORDER BY t.name
            `, letter)
		}
	case "A", "B", "C", "D", "E", "F", "G", "H", "I", "J", "K", "L", "M",
		"N", "O", "P", "Q", "R", "S", "T", "U", "V", "W", "X", "Y", "Z":
		tagRows, err = th.db.Query(`
            SELECT t.name, COUNT(bt.book_id) as book_count
            FROM tags t
            LEFT JOIN book_tags bt ON t.id = bt.tag_id
            WHERE UPPER(SUBSTR(t.name, 1, 1)) = ?
            GROUP BY t.name
            ORDER BY t.name
        `, strings.ToUpper(letter))
	default:
		tagRows, err = th.db.Query(`
            SELECT t.name, COUNT(bt.book_id) as book_count
            FROM tags t
            LEFT JOIN book_tags bt ON t.id = bt.tag_id
            WHERE UPPER(SUBSTR(t.name, 1, 1)) = UPPER(?)
            GROUP BY t.name
            ORDER BY t.name
        `, letter)
	}

	if err != nil {
		return nil, err
	}
	defer tagRows.Close()

	var tags []*TagInfo
	for tagRows.Next() {
		var tagName string
		var bookCount int
		if err := tagRows.Scan(&tagName, &bookCount); err != nil {
			continue
		}

		tagInfo := &TagInfo{
			Name:      tagName,
			BookCount: bookCount,
		}
		tags = append(tags, tagInfo)
	}

	return tags, tagRows.Err()
}

// getAllTags получает все теги
func (th *TagHandler) getAllTags() ([]*TagInfo, error) {
	tagRows, err := th.db.Query(`
        SELECT t.name, COUNT(bt.book_id) as book_count
        FROM tags t
        LEFT JOIN book_tags bt ON t.id = bt.tag_id
        GROUP BY t.name
        ORDER BY t.name
    `)
	if err != nil {
		return nil, err
	}
	defer tagRows.Close()

	var tags []*TagInfo
	for tagRows.Next() {
		var tagName string
		var bookCount int
		if err := tagRows.Scan(&tagName, &bookCount); err != nil {
			continue
		}

		tagInfo := &TagInfo{
			Name:      tagName,
			BookCount: bookCount,
		}
		tags = append(tags, tagInfo)
	}

	return tags, tagRows.Err()
}

// getBooksWithTag получает книги с определенным тегом
func (th *TagHandler) getBooksWithTag(tagName string) (map[int]*models.Book, error) {
	cfg := config.GetConfig()
	query := `
        SELECT DISTINCT b.id as book_id, b.title, b.series, b.series_number, b.published_at,
               b.isbn, b.year, b.publisher, b.file_url, b.file_type, b.file_hash
        FROM books b
        JOIN book_tags bt ON b.id = bt.book_id
        JOIN tags t ON bt.tag_id = t.id
        WHERE t.name = ?
          AND b.file_type IN ('epub', 'fb2', 'fb2.zip')
          AND (b.over18 IS NULL OR b.over18 = 0)
        ORDER BY b.title
    `

	bookRows, err := th.db.Query(query, tagName)
	if err != nil {
		return nil, err
	}
	defer bookRows.Close()

	booksMap := make(map[int]*models.Book)
	var bookIDs []int

	for bookRows.Next() {
		var bookID int
		var title, series, seriesNumber, publishedAt, isbn, year, publisher sql.NullString
		var fileURL, fileType, fileHash sql.NullString

		err := bookRows.Scan(&bookID, &title, &series, &seriesNumber, &publishedAt,
			&isbn, &year, &publisher, &fileURL, &fileType, &fileHash)
		if err != nil {
			if cfg.Debug {
				log.Printf("Ошибка сканирования строки книги: %v", err)
			}
			continue
		}

		annotationText := readAnnotation(fileHash.String)

		book := &models.Book{
			ID:           bookID,
			Title:        title.String,
			Authors:      []models.Author{},
			Series:       series.String,
			SeriesNumber: seriesNumber.String,
			PublishedAt:  publishedAt.String,
			Annotation:   annotationText,
			ISBN:         isbn.String,
			Year:         year.String,
			Publisher:    publisher.String,
			Files:        []models.BookFile{},
			Tags:         []string{tagName},
		}

		bookFile := models.BookFile{
			URL:      fmt.Sprintf("/opds-download/%d/%s", bookID, url.QueryEscape(title.String+"."+GetFileExtension(fileType.String))),
			Type:     fileType.String,
			FileHash: fileHash.String,
		}
		book.Files = append(book.Files, bookFile)

		booksMap[bookID] = book
		bookIDs = append(bookIDs, bookID)
	}

	// Получаем всех авторов одним запросом
	if len(bookIDs) > 0 {
		placeholders := th.CreatePlaceholders(len(bookIDs))
		authorQuery := fmt.Sprintf(`
            SELECT ba.book_id, a.full_name
            FROM book_authors ba
            JOIN authors a ON ba.author_id = a.id
            WHERE ba.book_id IN (%s)
            ORDER BY ba.book_id, a.full_name
        `, placeholders)

		authorArgs := th.ConvertToInterfaceSlice(bookIDs)
		authorRows, err := th.db.Query(authorQuery, authorArgs...)
		if err == nil {
			defer authorRows.Close()

			for authorRows.Next() {
				var bookID int
				var fullName string
				if err := authorRows.Scan(&bookID, &fullName); err != nil {
					continue
				}

				if book, exists := booksMap[bookID]; exists {
					author := models.Author{
						FullName: fullName,
					}
					book.Authors = append(book.Authors, author)
				}
			}
		}
	}

	// Получаем все теги для книг одним запросом
	if len(bookIDs) > 0 {
		placeholders := th.CreatePlaceholders(len(bookIDs))
		tagQuery := fmt.Sprintf(`
            SELECT bt.book_id, t.name
            FROM book_tags bt
            JOIN tags t ON bt.tag_id = t.id
            WHERE bt.book_id IN (%s)
            ORDER BY bt.book_id, t.name
        `, placeholders)

		tagArgs := th.ConvertToInterfaceSlice(bookIDs)
		tagRows, err := th.db.Query(tagQuery, tagArgs...)
		if err == nil {
			defer tagRows.Close()

			tagMap := make(map[int][]string)
			for tagRows.Next() {
				var bookID int
				var tagName string
				if err := tagRows.Scan(&bookID, &tagName); err != nil {
					continue
				}

				tagMap[bookID] = append(tagMap[bookID], tagName)
			}

			for bookID, tags := range tagMap {
				if book, exists := booksMap[bookID]; exists {
					book.Tags = tags
				}
			}
		}
	}

	return booksMap, nil
}

// renderTagsFeed создает и отправляет OPDS фид с тегами
func (th *TagHandler) renderTagsFeed(w http.ResponseWriter, title string, tags []*TagInfo) {
	var entries []models.Entry

	for _, tagInfo := range tags {
		entry := CreateNavigationEntry(
			tagInfo.Name,
			"turanga:tag:"+url.QueryEscape(tagInfo.Name),
			fmt.Sprintf("Книги с тегом \"%s\" (%d шт.)", tagInfo.Name, tagInfo.BookCount),
			"/tags/"+url.QueryEscape(tagInfo.Name),
			"subsection",
			"",
		)
		entries = append(entries, entry)
	}

	th.RenderNavigationFeed(w, title, entries)
}

// TagInfo вспомогательная структура для информации о теге
type TagInfo struct {
	Name      string
	BookCount int
}

// Вспомогательные функции для существующего API

// AddTagToBookRequest структура для запроса на добавление тега к книге
type AddTagToBookRequest struct {
	TagName string `json:"tag_name"`
}

// RemoveTagFromBookRequest структура для запроса на удаление тега из книги
type RemoveTagFromBookRequest struct {
	TagName string `json:"tag_name"`
}

// AddTagToBookHandler обрабатывает POST /tags/book/{id}
func (th *TagHandler) AddTagToBookHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Извлекаем ID книги из URL
	bookIDStr := strings.TrimPrefix(r.URL.Path, "/tags/book/")
	if bookIDStr == "" || bookIDStr == r.URL.Path {
		http.Error(w, "Book ID is required", http.StatusBadRequest)
		return
	}
	// Пытаемся преобразовать в число
	bookID, err := strconv.Atoi(bookIDStr)
	if err != nil {
		http.Error(w, "Invalid book ID", http.StatusBadRequest)
		return
	}

	// Декодируем JSON тело запроса
	var req AddTagToBookRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Проверяем, что тег не пустой и не длиннее 16 символов
	req.TagName = strings.TrimSpace(req.TagName)
	if req.TagName == "" {
		http.Error(w, "Tag name is required", http.StatusBadRequest)
		return
	}
	if len(req.TagName) > 16 {
		http.Error(w, "Tag name must be 16 characters or less", http.StatusBadRequest)
		return
	}

	// Проверяем, существует ли книга
	var exists bool
	err = th.db.QueryRow("SELECT EXISTS(SELECT 1 FROM books WHERE id = ?)", bookID).Scan(&exists)
	if err != nil {
		log.Printf("Database error checking book existence: %v", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	if !exists {
		http.Error(w, "Book not found", http.StatusNotFound)
		return
	}

	// Добавляем тег (если он еще не существует) и связываем с книгой
	// Используем транзакцию для атомарности
	tx, err := th.db.Begin()
	if err != nil {
		log.Printf("Error starting transaction: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback() // Откат в случае ошибки, если Commit не будет вызван

	// Вставляем тег, игнорируя дубликаты
	_, err = tx.Exec("INSERT OR IGNORE INTO tags (name) VALUES (?)", req.TagName)
	if err != nil {
		log.Printf("Error inserting tag: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Получаем ID тега (вставленного или существующего)
	var tagID int
	err = tx.QueryRow("SELECT id FROM tags WHERE name = ?", req.TagName).Scan(&tagID)
	if err != nil {
		log.Printf("Error getting tag ID: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Связываем книгу с тегом, игнорируя дубликаты
	_, err = tx.Exec("INSERT OR IGNORE INTO book_tags (book_id, tag_id) VALUES (?, ?)", bookID, tagID)
	if err != nil {
		log.Printf("Error linking book to tag: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Коммитим транзакцию
	if err = tx.Commit(); err != nil {
		log.Printf("Error committing transaction: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Возвращаем успешный ответ
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"message": fmt.Sprintf("Tag '%s' added to book %d", req.TagName, bookID)})
	// log.Printf("Added tag '%s' to book %d", req.TagName, bookID)
}

// RemoveTagFromBookHandler обрабатывает DELETE /tags/book/{id}
func (th *TagHandler) RemoveTagFromBookHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Извлекаем ID книги из URL
	bookIDStr := strings.TrimPrefix(r.URL.Path, "/tags/book/")
	if bookIDStr == "" || bookIDStr == r.URL.Path {
		http.Error(w, "Book ID is required", http.StatusBadRequest)
		return
	}
	// Пытаемся преобразовать в число
	bookID, err := strconv.Atoi(bookIDStr)
	if err != nil {
		http.Error(w, "Invalid book ID", http.StatusBadRequest)
		return
	}

	// Декодируем JSON тело запроса
	var req RemoveTagFromBookRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Проверяем, что тег не пустой
	req.TagName = strings.TrimSpace(req.TagName)
	if req.TagName == "" {
		http.Error(w, "Tag name is required", http.StatusBadRequest)
		return
	}

	// Проверяем, существует ли книга
	var exists bool
	err = th.db.QueryRow("SELECT EXISTS(SELECT 1 FROM books WHERE id = ?)", bookID).Scan(&exists)
	if err != nil {
		log.Printf("Database error checking book existence: %v", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	if !exists {
		http.Error(w, "Book not found", http.StatusNotFound)
		return
	}

	// Удаляем связь книги с тегом
	result, err := th.db.Exec(`
        DELETE FROM book_tags 
        WHERE book_id = ? AND tag_id IN (SELECT id FROM tags WHERE name = ?)
    `, bookID, req.TagName)
	if err != nil {
		log.Printf("Error removing tag from book: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	rowsAffected, _ := result.RowsAffected() // Игнорируем ошибку, так как она не критична
	if rowsAffected == 0 {
		// Это может означать, что тег не был связан с книгой или тег не существует
		// В любом случае, результат - тег удален (или не был связан)
		// Можно вернуть 200 OK или 404 Not Found. Выберем 200 OK для идемпотентности.
		// http.Error(w, "Tag not found for this book", http.StatusNotFound)
		// return
	}

	// Возвращаем успешный ответ
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"message": fmt.Sprintf("Tag '%s' removed from book %d", req.TagName, bookID)})
	// log.Printf("Removed tag '%s' from book %d", req.TagName, bookID)
}

// GetBookTagsHandler обрабатывает GET /tags/book/{id}
func (th *TagHandler) GetBookTagsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Извлекаем ID книги из URL
	bookIDStr := strings.TrimPrefix(r.URL.Path, "/tags/book/")
	if bookIDStr == "" || bookIDStr == r.URL.Path {
		http.Error(w, "Book ID is required", http.StatusBadRequest)
		return
	}
	// Пытаемся преобразовать в число
	bookID, err := strconv.Atoi(bookIDStr)
	if err != nil {
		http.Error(w, "Invalid book ID", http.StatusBadRequest)
		return
	}

	// Проверяем, существует ли книга
	var exists bool
	err = th.db.QueryRow("SELECT EXISTS(SELECT 1 FROM books WHERE id = ?)", bookID).Scan(&exists)
	if err != nil {
		log.Printf("Database error checking book existence: %v", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	if !exists {
		http.Error(w, "Book not found", http.StatusNotFound)
		return
	}

	// Получаем теги книги
	rows, err := th.db.Query(`
        SELECT t.name 
        FROM tags t 
        JOIN book_tags bt ON t.id = bt.tag_id 
        WHERE bt.book_id = ?
        ORDER BY t.name
    `, bookID)
	if err != nil {
		log.Printf("Database error getting book tags: %v", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var tags []string
	for rows.Next() {
		var tagName string
		if err := rows.Scan(&tagName); err != nil {
			continue
		}
		tags = append(tags, tagName)
	}

	// Возвращаем список тегов
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tags)
}

// GetAllTagsHandler обрабатывает GET /tags
func (th *TagHandler) GetAllTagsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Получаем все теги
	rows, err := th.db.Query("SELECT name FROM tags ORDER BY name")
	if err != nil {
		log.Printf("Database error getting all tags: %v", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var tags []string
	for rows.Next() {
		var tagName string
		if err := rows.Scan(&tagName); err != nil {
			continue
		}
		tags = append(tags, tagName)
	}

	// Возвращаем список всех тегов
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tags)
}

// HandleTagBookRequest обрабатывает запросы к /tags/book/{id}
func (th *TagHandler) HandleTagBookRequest(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		th.AddTagToBookHandler(w, r)
	case http.MethodDelete:
		th.RemoveTagFromBookHandler(w, r)
	case http.MethodGet:
		th.GetBookTagsHandler(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}
