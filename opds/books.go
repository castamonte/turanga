// opds/books.go

package opds

import (
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"strings"
	"turanga/config"
)

// BookHandler отвечает за обработку запросов к /books
type BookHandler struct {
	*BaseHandler
}

// NewBookHandler создает новый экземпляр BookHandler
func NewBookHandler(database *sql.DB, cfg *config.Config) *BookHandler {
	return &BookHandler{
		BaseHandler: NewBaseHandler(database, cfg),
	}
}

// BooksHandler обрабатывает запрос к /books
func (bh *BookHandler) BooksHandler(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/books")

	if path != "" && path != "/" {
		letterPath := strings.TrimPrefix(path, "/")
		if len([]rune(letterPath)) == 1 {
			bh.showBooksForLetter(w, r, letterPath)
			return
		}
	}

	// Подсчитываем книги
	bookCount, err := bh.CountItems(`
        SELECT COUNT(*)
        FROM books b
        WHERE b.file_type IN ('epub', 'fb2', 'fb2.zip')
          AND (b.over18 IS NULL OR b.over18 = 0)
    `)
	if err != nil {
		log.Printf("Ошибка подсчета книг: %v", err)
		http.Error(w, "Database error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Если книг больше порога, показываем группировку по буквам
	if bh.ShouldPaginate(bookCount) {
		bh.showBooksLetters(w, r)
		return
	}

	// Иначе показываем все книги
	bh.showAllBooks(w, r)
}

// showBooksLetters показывает каталог с буквами алфавита для книг
func (bh *BookHandler) showBooksLetters(w http.ResponseWriter, r *http.Request) {
	query := `
        SELECT DISTINCT 
            CASE 
                WHEN SUBSTR(b.title_lower, 1, 1) BETWEEN 'a' AND 'z' THEN SUBSTR(b.title_lower, 1, 1)
                WHEN SUBSTR(b.title_lower, 1, 1) BETWEEN 'а' AND 'я' THEN SUBSTR(b.title_lower, 1, 1)
                WHEN SUBSTR(b.title_lower, 1, 1) = 'ё' THEN 'ё'
                ELSE SUBSTR(b.title_lower, 1, 1)
            END as first_letter, 
            COUNT(*) as book_count
        FROM books b
        WHERE b.file_type IN ('epub', 'fb2', 'fb2.zip')
          AND (b.over18 IS NULL OR b.over18 = 0)
        GROUP BY 
            CASE 
                WHEN SUBSTR(b.title_lower, 1, 1) BETWEEN 'a' AND 'z' THEN SUBSTR(b.title_lower, 1, 1)
                WHEN SUBSTR(b.title_lower, 1, 1) BETWEEN 'а' AND 'я' THEN SUBSTR(b.title_lower, 1, 1)
                WHEN SUBSTR(b.title_lower, 1, 1) = 'ё' THEN 'ё'
                ELSE SUBSTR(b.title_lower, 1, 1)
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

	letterRows, err := bh.db.Query(query)
	if err != nil {
		log.Printf("Ошибка запроса букв книг: %v", err)
		http.Error(w, "Database error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer letterRows.Close()

	entries, err := CreateAlphabetEntries(letterRows, "/books", "Книги")
	if err != nil {
		log.Printf("Ошибка создания записей букв: %v", err)
		http.Error(w, "Database error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	bh.RenderNavigationFeed(w, "Книги по алфавиту", entries)
}

// showBooksForLetter показывает книги на определенную букву
func (bh *BookHandler) showBooksForLetter(w http.ResponseWriter, r *http.Request, letter string) {
	if letter == "" {
		http.Error(w, "Invalid letter", http.StatusBadRequest)
		return
	}

	booksMap, err := GetBooksByLetter(bh.db, letter, "title")
	if err != nil {
		log.Printf("Ошибка получения книг на букву %s: %v", letter, err)
		http.Error(w, "Database error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	sortedBooks := bh.SortBooksByTitle(booksMap)
	bh.RenderAcquisitionFeed(w, fmt.Sprintf("Книги на букву \"%s\"", letter), sortedBooks)
}

// showAllBooks показывает все книги (для случаев, когда их <= 60)
func (bh *BookHandler) showAllBooks(w http.ResponseWriter, r *http.Request) {
	query := `
        SELECT b.id as book_id, b.title, b.series, b.series_number, b.published_at,
               b.isbn, b.year, b.publisher, b.file_url, b.file_type, b.file_hash
        FROM books b
        WHERE b.file_type IN ('epub', 'fb2', 'fb2.zip')
          AND (b.over18 IS NULL OR b.over18 = 0)
        ORDER BY b.title_lower
        LIMIT 1000
    `

	booksMap, err := bh.GetBooksWithAuthors(query)
	if err != nil {
		log.Printf("Ошибка получения книг: %v", err)
		http.Error(w, "Database error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	sortedBooks := bh.SortBooksByTitle(booksMap)
	bh.RenderAcquisitionFeed(w, "Все книги", sortedBooks)
}

// RecentHandler обрабатывает запрос к /recent
func (bh *BookHandler) RecentHandler(w http.ResponseWriter, r *http.Request) {
	query := `
        SELECT b.id as book_id, b.title, b.series, b.series_number, b.published_at,
               b.isbn, b.year, b.publisher, b.file_url, b.file_type, b.file_hash
        FROM books b
        WHERE b.file_type IN ('epub', 'fb2', 'fb2.zip')
          AND (b.over18 IS NULL OR b.over18 = 0)
        ORDER BY b.id DESC
        LIMIT 60
    `

	booksMap, err := bh.GetBooksWithAuthors(query)
	if err != nil {
		log.Printf("Ошибка получения последних книг: %v", err)
		http.Error(w, "Database error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	sortedBooks := bh.SortBooksByID(booksMap)
	bh.RenderAcquisitionFeed(w, "Новые поступления", sortedBooks)
}
