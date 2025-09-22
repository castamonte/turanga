package opds

import (
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"turanga/config"
	"turanga/models"
)

// AuthorHandler отвечает за обработку запросов к /authors
type AuthorHandler struct {
	*BaseHandler
}

// NewAuthorHandler создает новый экземпляр AuthorHandler
func NewAuthorHandler(database *sql.DB, cfg *config.Config) *AuthorHandler {
	return &AuthorHandler{
		BaseHandler: NewBaseHandler(database, cfg),
	}
}

// AuthorsHandler обрабатывает запрос к /authors
func (ah *AuthorHandler) AuthorsHandler(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/authors")

	if path != "" && path != "/" {
		trimmedPath := strings.TrimPrefix(path, "/")
		if len([]rune(trimmedPath)) == 1 {
			ah.AuthorsByLetterHandler(w, r)
			return
		}
		ah.AuthorBooksHandler(w, r)
		return
	}

	// Получаем количество авторов с книгами
	authorCount, err := ah.CountItems(`
        SELECT COUNT(DISTINCT a.id)
        FROM authors a
        JOIN book_authors ba ON a.id = ba.author_id
        JOIN books b ON ba.book_id = b.id
        WHERE a.full_name IS NOT NULL AND a.full_name != ''
          AND a.last_name IS NOT NULL AND a.last_name != ''
          AND b.file_type IN ('epub', 'fb2', 'fb2.zip')
          AND (b.over18 IS NULL OR b.over18 = 0)
    `)
	if err != nil {
		log.Printf("Ошибка подсчета авторов: %v", err)
		http.Error(w, "Database error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Если авторов больше порога, показываем группировку по буквам
	if ah.ShouldPaginate(authorCount) {
		ah.AuthorsLettersHandler(w, r)
		return
	}

	// Иначе показываем всех авторов
	ah.AuthorsListHandler(w, r)
}

// AuthorsLettersHandler показывает каталог с буквами алфавита
func (ah *AuthorHandler) AuthorsLettersHandler(w http.ResponseWriter, r *http.Request) {
	query := `
        SELECT DISTINCT 
            CASE 
                WHEN UPPER(SUBSTR(a.last_name, 1, 1)) BETWEEN 'A' AND 'Z' THEN UPPER(SUBSTR(a.last_name, 1, 1))
                WHEN SUBSTR(a.last_name, 1, 1) BETWEEN 'А' AND 'Я' THEN SUBSTR(a.last_name, 1, 1)
                WHEN SUBSTR(a.last_name, 1, 1) = 'Ё' THEN 'Ё'
                ELSE UPPER(SUBSTR(a.last_name, 1, 1))
            END as first_letter, 
            COUNT(DISTINCT a.id) as author_count
        FROM authors a
        JOIN book_authors ba ON a.id = ba.author_id
        JOIN books b ON ba.book_id = b.id
        WHERE a.full_name IS NOT NULL AND a.full_name != ''
          AND a.last_name IS NOT NULL AND a.last_name != ''
          AND b.file_type IN ('epub', 'fb2', 'fb2.zip')
          AND (b.over18 IS NULL OR b.over18 = 0)
        GROUP BY 
            CASE 
                WHEN UPPER(SUBSTR(a.last_name, 1, 1)) BETWEEN 'A' AND 'Z' THEN UPPER(SUBSTR(a.last_name, 1, 1))
                WHEN SUBSTR(a.last_name, 1, 1) BETWEEN 'А' AND 'Я' THEN SUBSTR(a.last_name, 1, 1)
                WHEN SUBSTR(a.last_name, 1, 1) = 'Ё' THEN 'Ё'
                ELSE UPPER(SUBSTR(a.last_name, 1, 1))
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

	letterRows, err := ah.db.Query(query)
	if err != nil {
		log.Printf("Ошибка запроса букв авторов: %v", err)
		http.Error(w, "Database error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer letterRows.Close()

	entries, err := CreateAlphabetEntries(letterRows, "/authors", "Авторы")
	if err != nil {
		log.Printf("Ошибка создания записей букв: %v", err)
		http.Error(w, "Database error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	ah.RenderNavigationFeed(w, "Авторы по алфавиту", entries)
}

// AuthorsByLetterHandler показывает авторов на определенную букву
func (ah *AuthorHandler) AuthorsByLetterHandler(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/authors/")
	letter := strings.TrimPrefix(path, "/")

	if letter == "" || strings.Contains(letter, "/") {
		letter = string([]rune(path)[0])
	}

	authors, bookCounts, err := ah.getAuthorsByLetter(letter)
	if err != nil {
		log.Printf("Ошибка запроса авторов на букву %s: %v", letter, err)
		http.Error(w, "Database error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	ah.renderAuthorsFeed(w, fmt.Sprintf("Авторы на букву \"%s\"", letter), authors, bookCounts)
}

// AuthorsListHandler показывает всех авторов (для случаев, когда их <= 60)
func (ah *AuthorHandler) AuthorsListHandler(w http.ResponseWriter, r *http.Request) {
	authors, bookCounts, err := ah.getAllAuthors()
	if err != nil {
		log.Printf("Ошибка запроса списка авторов к БД: %v", err)
		http.Error(w, "Database error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	ah.renderAuthorsFeed(w, "Авторы", authors, bookCounts)
}

// AuthorBooksHandler обрабатывает запрос к конкретному автору
func (ah *AuthorHandler) AuthorBooksHandler(w http.ResponseWriter, r *http.Request) {
	authorName := strings.TrimPrefix(r.URL.Path, "/authors/")
	var err error
	authorName, err = url.QueryUnescape(authorName)
	if err != nil {
		authorName = strings.TrimPrefix(r.URL.Path, "/authors/")
	}

	books, err := ah.getBooksByAuthor(authorName)
	if err != nil {
		log.Printf("Ошибка запроса книг автора к БД: %v", err)
		http.Error(w, "Database error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	sortedBooks := ah.sortBooksByTitle(books)
	ah.RenderAcquisitionFeed(w, "Книги автора: "+authorName, sortedBooks)
}

// getAuthorsByLetter получает авторов на определенную букву
func (ah *AuthorHandler) getAuthorsByLetter(letter string) ([]*models.Author, map[string]int, error) {
	cfg := config.GetConfig()
	var authorRows *sql.Rows
	var err error

	switch letter {
	case "А", "Б", "В", "Г", "Д", "Е", "Ж", "З", "И", "Й", "К", "Л", "М",
		"Н", "О", "П", "Р", "С", "Т", "У", "Ф", "Х", "Ц", "Ч", "Ш", "Щ", "Ъ", "Ы", "Ь", "Э", "Ю", "Я":
		authorRows, err = ah.db.Query(`
            SELECT a.last_name, a.full_name, COUNT(DISTINCT b.id) as book_count 
            FROM authors a
            JOIN book_authors ba ON a.id = ba.author_id
            JOIN books b ON ba.book_id = b.id
            WHERE a.full_name IS NOT NULL AND a.full_name != ''
              AND a.last_name IS NOT NULL AND a.last_name != ''
              AND SUBSTR(a.last_name, 1, 1) = ?
              AND b.file_type IN ('epub', 'fb2', 'fb2.zip')
              AND (b.over18 IS NULL OR b.over18 = 0)
            GROUP BY a.last_name, a.full_name
            ORDER BY a.last_name, a.full_name
        `, letter)
	case "Ё":
		authorRows, err = ah.db.Query(`
            SELECT a.last_name, a.full_name, COUNT(DISTINCT b.id) as book_count 
            FROM authors a
            JOIN book_authors ba ON a.id = ba.author_id
            JOIN books b ON ba.book_id = b.id
            WHERE a.full_name IS NOT NULL AND a.full_name != ''
              AND a.last_name IS NOT NULL AND a.last_name != ''
              AND (SUBSTR(a.last_name, 1, 1) = 'Ё' OR SUBSTR(a.last_name, 1, 1) = 'Е')
              AND b.file_type IN ('epub', 'fb2', 'fb2.zip')
              AND (b.over18 IS NULL OR b.over18 = 0)
            GROUP BY a.last_name, a.full_name
            ORDER BY a.last_name, a.full_name
        `)
	default:
		authorRows, err = ah.db.Query(`
            SELECT a.last_name, a.full_name, COUNT(DISTINCT b.id) as book_count 
            FROM authors a
            JOIN book_authors ba ON a.id = ba.author_id
            JOIN books b ON ba.book_id = b.id
            WHERE a.full_name IS NOT NULL AND a.full_name != ''
              AND a.last_name IS NOT NULL AND a.last_name != ''
              AND UPPER(SUBSTR(a.last_name, 1, 1)) = UPPER(?)
              AND b.file_type IN ('epub', 'fb2', 'fb2.zip')
              AND (b.over18 IS NULL OR b.over18 = 0)
            GROUP BY a.last_name, a.full_name
            ORDER BY a.last_name, a.full_name
        `, letter)
	}

	if err != nil {
		return nil, nil, err
	}
	defer authorRows.Close()

	var authors []*models.Author
	bookCounts := make(map[string]int)

	for authorRows.Next() {
		var lastName, fullName string
		var bookCount int
		if err := authorRows.Scan(&lastName, &fullName, &bookCount); err != nil {
			if cfg.Debug {
				log.Printf("Ошибка сканирования автора: %v", err)
			}
			continue
		}

		author := &models.Author{
			LastName: lastName,
			FullName: fullName,
		}
		authors = append(authors, author)
		bookCounts[fullName] = bookCount
	}

	return authors, bookCounts, authorRows.Err()
}

// getAllAuthors получает всех авторов
func (ah *AuthorHandler) getAllAuthors() ([]*models.Author, map[string]int, error) {
	cfg := config.GetConfig()
	authorRows, err := ah.db.Query(`
        SELECT a.last_name, a.full_name, COUNT(DISTINCT b.id) as book_count 
        FROM authors a
        JOIN book_authors ba ON a.id = ba.author_id
        JOIN books b ON ba.book_id = b.id
        WHERE a.full_name IS NOT NULL AND a.full_name != ''
          AND a.last_name IS NOT NULL AND a.last_name != ''
          AND b.file_type IN ('epub', 'fb2', 'fb2.zip')
          AND (b.over18 IS NULL OR b.over18 = 0)
        GROUP BY a.last_name, a.full_name
        ORDER BY a.last_name, a.full_name
    `)
	if err != nil {
		return nil, nil, err
	}
	defer authorRows.Close()

	var authors []*models.Author
	bookCounts := make(map[string]int)

	for authorRows.Next() {
		var lastName, fullName string
		var bookCount int
		if err := authorRows.Scan(&lastName, &fullName, &bookCount); err != nil {
			if cfg.Debug {
				log.Printf("Ошибка сканирования автора: %v", err)
			}
			continue
		}

		author := &models.Author{
			LastName: lastName,
			FullName: fullName,
		}
		authors = append(authors, author)
		bookCounts[fullName] = bookCount
	}

	return authors, bookCounts, authorRows.Err()
}

// getBooksByAuthor получает книги автора
func (ah *AuthorHandler) getBooksByAuthor(authorName string) (map[int]*models.Book, error) {
	cfg := config.GetConfig()
	query := `
        SELECT DISTINCT b.id as book_id, b.title, b.series, b.series_number, b.published_at,
               b.isbn, b.year, b.publisher,
               b.file_url, b.file_type, b.file_hash
        FROM books b
        WHERE b.id IN (
            SELECT DISTINCT b2.id
            FROM books b2
            JOIN book_authors ba2 ON b2.id = ba2.book_id
            JOIN authors a2 ON ba2.author_id = a2.id
            WHERE (a2.full_name = ? OR a2.full_name LIKE ?)
              AND b2.file_type IN ('epub', 'fb2', 'fb2.zip')
              AND (b2.over18 IS NULL OR b2.over18 = 0)
        )
        AND b.file_type IN ('epub', 'fb2', 'fb2.zip')
        AND (b.over18 IS NULL OR b.over18 = 0)
        ORDER BY b.title, 
                 (SELECT MIN(a3.last_name) 
                  FROM book_authors ba3 
                  JOIN authors a3 ON ba3.author_id = a3.id 
                  WHERE ba3.book_id = b.id)
    `

	bookRows, err := ah.db.Query(query, authorName, "%"+authorName+"%")
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
			&isbn, &year, &publisher,
			&fileURL, &fileType, &fileHash)
		if err != nil {
			if cfg.Debug {
				log.Printf("Ошибка сканирования строки для автора: %v", err)
			}
			continue
		}

		annotation := readAnnotation(fileHash.String)

		book := &models.Book{
			ID:           bookID,
			Title:        title.String,
			Authors:      []models.Author{},
			Series:       series.String,
			SeriesNumber: seriesNumber.String,
			PublishedAt:  publishedAt.String,
			Annotation:   annotation,
			ISBN:         isbn.String,
			Year:         year.String,
			Publisher:    publisher.String,
			Files:        []models.BookFile{},
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
		placeholders := ah.CreatePlaceholders(len(bookIDs))
		authorQuery := fmt.Sprintf(`
            SELECT ba.book_id, a.full_name
            FROM book_authors ba
            JOIN authors a ON ba.author_id = a.id
            WHERE ba.book_id IN (%s)
            ORDER BY ba.book_id, a.full_name
        `, placeholders)

		authorArgs := ah.ConvertToInterfaceSlice(bookIDs)
		authorRows, err := ah.db.Query(authorQuery, authorArgs...)
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

	return booksMap, nil
}

// renderAuthorsFeed создает и отправляет OPDS фид с авторами
func (ah *AuthorHandler) renderAuthorsFeed(w http.ResponseWriter, title string, authors []*models.Author, bookCounts map[string]int) {
	var entries []models.Entry

	for _, author := range authors {
		bookCount := bookCounts[author.FullName]
		authorContent := fmt.Sprintf("%s (%d книг)", author.FullName, bookCount)

		entry := CreateNavigationEntry(
			author.FullName,
			"turanga:author:"+url.QueryEscape(author.FullName),
			authorContent,
			"/authors/"+url.QueryEscape(author.FullName),
			"subsection",
			"",
		)
		entries = append(entries, entry)
	}

	ah.RenderNavigationFeed(w, title, entries)
}

// sortBooksByTitle сортирует книги по названию
func (ah *AuthorHandler) sortBooksByTitle(booksMap map[int]*models.Book) []*models.Book {
	return SortBooksByTitle(booksMap)
}
