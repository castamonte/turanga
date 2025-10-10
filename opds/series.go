package opds

import (
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"turanga/config"
	"turanga/models"
)

// SeriesHandler отвечает за обработку запросов к /series
type SeriesHandler struct {
	*BaseHandler
}

// NewSeriesHandler создает новый экземпляр SeriesHandler
func NewSeriesHandler(database *sql.DB, cfg *config.Config) *SeriesHandler {
	return &SeriesHandler{
		BaseHandler: NewBaseHandler(database, cfg),
	}
}

// SeriesHandler обрабатывает запрос к /series
func (sh *SeriesHandler) SeriesHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/series" && r.URL.Path != "/series/" {
		path := strings.TrimPrefix(r.URL.Path, "/series/")
		path = strings.TrimPrefix(path, "/")

		if len([]rune(path)) == 1 {
			sh.SeriesByLetterHandler(w, r)
			return
		}

		sh.SeriesBooksHandler(w, r)
		return
	}

	// Подсчитываем серии
	seriesCount, err := sh.CountItems(`
        SELECT COUNT(DISTINCT b.series)
        FROM books b 
        WHERE b.series != '' AND b.series IS NOT NULL 
          AND b.file_type IN ('epub', 'fb2', 'fb2.zip')
          AND (b.over18 IS NULL OR b.over18 = 0)
    `)
	if err != nil {
		log.Printf("Ошибка подсчета серий: %v", err)
		http.Error(w, "Database error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Если серий больше порога, показываем группировку по буквам
	if sh.ShouldPaginate(seriesCount) {
		sh.SeriesLettersHandler(w, r)
		return
	}

	sh.SeriesListHandler(w, r)
}

// SeriesLettersHandler показывает каталог с буквами алфавита для серий
func (sh *SeriesHandler) SeriesLettersHandler(w http.ResponseWriter, r *http.Request) {
	query := `
        SELECT DISTINCT 
            CASE 
                WHEN SUBSTR(b.series_lower, 1, 1) BETWEEN 'a' AND 'z' THEN SUBSTR(b.series_lower, 1, 1)
                WHEN SUBSTR(b.series_lower, 1, 1) BETWEEN 'а' AND 'я' THEN SUBSTR(b.series_lower, 1, 1)
                WHEN SUBSTR(b.series_lower, 1, 1) = 'ё' THEN 'ё'
                ELSE SUBSTR(b.series_lower, 1, 1)
            END as first_letter, 
            COUNT(DISTINCT b.series) as series_count
        FROM books b
        WHERE b.series_lower != '' AND b.series_lower IS NOT NULL 
          AND b.file_type IN ('epub', 'fb2', 'fb2.zip')
          AND (b.over18 IS NULL OR b.over18 = 0)
        GROUP BY 
            CASE 
                WHEN SUBSTR(b.series_lower, 1, 1) BETWEEN 'a' AND 'z' THEN SUBSTR(b.series_lower, 1, 1)
                WHEN SUBSTR(b.series_lower, 1, 1) BETWEEN 'а' AND 'я' THEN SUBSTR(b.series_lower, 1, 1)
                WHEN SUBSTR(b.series_lower, 1, 1) = 'ё' THEN 'ё'
                ELSE SUBSTR(b.series_lower, 1, 1)
            END
        ORDER BY 
            CASE 
                WHEN first_letter BETWEEN 'a' AND 'z' THEN 1
                WHEN first_letter BETWEEN 'а' AND 'я' THEN 2
                WHEN first_letter = 'ё' THEN 3
                ELSE 4
            END,
            first_letter
    `

	letterRows, err := sh.db.Query(query)
	if err != nil {
		log.Printf("Ошибка запроса букв серий: %v", err)
		http.Error(w, "Database error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer letterRows.Close()

	entries, err := CreateAlphabetEntries(letterRows, "/series", "Серии")
	if err != nil {
		log.Printf("Ошибка создания записей букв: %v", err)
		http.Error(w, "Database error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	sh.RenderNavigationFeed(w, "Серии по алфавиту", entries)
}

// SeriesByLetterHandler показывает серии на определенную букву
func (sh *SeriesHandler) SeriesByLetterHandler(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/series/")
	letter := strings.TrimPrefix(path, "/")

	if letter == "" && path != "" {
		if len([]rune(path)) > 0 {
			letter = string([]rune(path)[0])
		}
	}

	if letter == "" {
		http.Error(w, "Invalid letter", http.StatusBadRequest)
		return
	}

	seriesList, err := sh.getSeriesByLetter(letter)
	if err != nil {
		log.Printf("Ошибка запроса серий на букву %s: %v", letter, err)
		http.Error(w, "Database error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	sh.renderSeriesFeed(w, fmt.Sprintf("Серии на букву \"%s\"", letter), seriesList)
}

// SeriesListHandler показывает все серии (для случаев, когда их <= 60)
func (sh *SeriesHandler) SeriesListHandler(w http.ResponseWriter, r *http.Request) {
	seriesList, err := sh.getAllSeries()
	if err != nil {
		log.Printf("Ошибка запроса серий к БД: %v", err)
		http.Error(w, "Database error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	sh.renderSeriesFeed(w, "Серии", seriesList)
}

// SeriesBooksHandler обрабатывает запрос к конкретной серии
func (sh *SeriesHandler) SeriesBooksHandler(w http.ResponseWriter, r *http.Request) {
	seriesName := strings.TrimPrefix(r.URL.Path, "/series/")
	var err error
	seriesName, err = url.QueryUnescape(seriesName)
	if err != nil {
		seriesName = strings.TrimPrefix(r.URL.Path, "/series/")
	}

	books, err := sh.getBooksBySeries(seriesName)
	if err != nil {
		log.Printf("Ошибка запроса книг серии к БД: %v", err)
		http.Error(w, "Database error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	sortedBooks := sh.sortBooksBySeries(books)
	sh.RenderAcquisitionFeed(w, "Серия: "+seriesName, sortedBooks)
}

// getSeriesByLetter получает серии на определенную букву
func (sh *SeriesHandler) getSeriesByLetter(letter string) ([]*SeriesInfo, error) {
	var seriesRows *sql.Rows
	var err error

	// Используем lower-версию буквы для поиска
	lowerLetter := strings.ToLower(letter)

	switch lowerLetter {
	case "а", "б", "в", "г", "д", "е", "ж", "з", "и", "й", "к", "л", "м",
		"н", "о", "п", "р", "с", "т", "у", "ф", "х", "ц", "ч", "ш", "щ", "ъ", "ы", "ь", "э", "ю", "я":
		if lowerLetter == "ё" {
			seriesRows, err = sh.db.Query(`
                SELECT b.series, COUNT(*) as book_count 
                FROM books b 
                WHERE b.series_lower != '' AND b.series_lower IS NOT NULL 
                  AND (SUBSTR(b.series_lower, 1, 1) = 'ё' OR SUBSTR(b.series_lower, 1, 1) = 'е')
                  AND b.file_type IN ('epub', 'fb2', 'fb2.zip')
                  AND (b.over18 IS NULL OR b.over18 = 0)
                GROUP BY b.series 
                ORDER BY b.series_lower
            `)
		} else {
			seriesRows, err = sh.db.Query(`
                SELECT b.series, COUNT(*) as book_count 
                FROM books b 
                WHERE b.series_lower != '' AND b.series_lower IS NOT NULL 
                  AND SUBSTR(b.series_lower, 1, 1) = ?
                  AND b.file_type IN ('epub', 'fb2', 'fb2.zip')
                  AND (b.over18 IS NULL OR b.over18 = 0)
                GROUP BY b.series 
                ORDER BY b.series_lower
            `, lowerLetter)
		}
	default:
		seriesRows, err = sh.db.Query(`
            SELECT b.series, COUNT(*) as book_count 
            FROM books b 
            WHERE b.series_lower != '' AND b.series_lower IS NOT NULL 
              AND SUBSTR(b.series_lower, 1, 1) = ?
              AND b.file_type IN ('epub', 'fb2', 'fb2.zip')
              AND (b.over18 IS NULL OR b.over18 = 0)
            GROUP BY b.series 
            ORDER BY b.series_lower
        `, lowerLetter)
	}

	if err != nil {
		return nil, err
	}
	defer seriesRows.Close()

	var seriesList []*SeriesInfo
	for seriesRows.Next() {
		var seriesName string
		var bookCount int
		if err := seriesRows.Scan(&seriesName, &bookCount); err != nil {
			continue
		}

		seriesInfo := &SeriesInfo{
			Name:      seriesName,
			BookCount: bookCount,
		}
		seriesList = append(seriesList, seriesInfo)
	}

	return seriesList, seriesRows.Err()
}

// getAllSeries получает все серии
func (sh *SeriesHandler) getAllSeries() ([]*SeriesInfo, error) {
	seriesRows, err := sh.db.Query(`
        SELECT b.series, COUNT(*) as book_count 
        FROM books b 
        WHERE b.series_lower != '' AND b.series_lower IS NOT NULL 
          AND b.file_type IN ('epub', 'fb2', 'fb2.zip')
          AND (b.over18 IS NULL OR b.over18 = 0)
        GROUP BY b.series 
        ORDER BY b.series_lower
    `)
	if err != nil {
		return nil, err
	}
	defer seriesRows.Close()

	var seriesList []*SeriesInfo
	for seriesRows.Next() {
		var seriesName string
		var bookCount int
		if err := seriesRows.Scan(&seriesName, &bookCount); err != nil {
			continue
		}

		seriesInfo := &SeriesInfo{
			Name:      seriesName,
			BookCount: bookCount,
		}
		seriesList = append(seriesList, seriesInfo)
	}

	return seriesList, seriesRows.Err()
}

// getBooksBySeries получает книги серии с авторами
func (sh *SeriesHandler) getBooksBySeries(seriesName string) (map[int]*models.Book, error) {
	cfg := config.GetConfig()
	// Используем lower-версию имени серии для поиска
	lowerSeriesName := strings.ToLower(seriesName)
	query := `
        SELECT DISTINCT b.id as book_id, b.title, b.series, b.series_number, b.published_at,
               b.isbn, b.year, b.publisher,
               b.file_url, b.file_type, b.file_hash
        FROM books b
        WHERE b.series_lower = ?
          AND b.file_type IN ('epub', 'fb2', 'fb2.zip')
          AND (b.over18 IS NULL OR b.over18 = 0)
        ORDER BY 
            CASE 
                WHEN b.series_number GLOB '[0-9]*' THEN CAST(b.series_number AS INTEGER)
                ELSE 0
            END,
            b.series_number,
            b.title_lower
    `

	bookRows, err := sh.db.Query(query, lowerSeriesName)
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
				log.Printf("Ошибка сканирования строки для серии: %v", err)
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
		placeholders := sh.CreatePlaceholders(len(bookIDs))
		authorQuery := fmt.Sprintf(`
            SELECT ba.book_id, a.full_name
            FROM book_authors ba
            JOIN authors a ON ba.author_id = a.id
            WHERE ba.book_id IN (%s)
            ORDER BY ba.book_id, a.full_name
        `, placeholders)

		authorArgs := sh.ConvertToInterfaceSlice(bookIDs)
		authorRows, err := sh.db.Query(authorQuery, authorArgs...)
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

// renderSeriesFeed создает и отправляет OPDS фид с сериями
func (sh *SeriesHandler) renderSeriesFeed(w http.ResponseWriter, title string, seriesList []*SeriesInfo) {
	var entries []models.Entry

	for _, seriesInfo := range seriesList {
		entry := CreateNavigationEntry(
			seriesInfo.Name,
			"turanga:series:"+url.QueryEscape(seriesInfo.Name),
			fmt.Sprintf("Книги серии \"%s\" (%d шт.)", seriesInfo.Name, seriesInfo.BookCount),
			"/series/"+url.QueryEscape(seriesInfo.Name),
			"subsection",
			"",
		)
		entries = append(entries, entry)
	}

	sh.RenderNavigationFeed(w, title, entries)
}

// sortBooksBySeries сортирует книги по серии и номеру
func (sh *SeriesHandler) sortBooksBySeries(booksMap map[int]*models.Book) []*models.Book {
	var books []*models.Book
	for _, book := range booksMap {
		books = append(books, book)
	}

	sort.Slice(books, func(i, j int) bool {
		if books[i].Series != books[j].Series {
			return books[i].Series < books[j].Series
		}
		return books[i].SeriesNumber < books[j].SeriesNumber
	})

	return books
}

// SeriesInfo вспомогательная структура для информации о серии
type SeriesInfo struct {
	Name      string
	BookCount int
}
