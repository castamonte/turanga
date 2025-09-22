package opds

import (
	"database/sql"
	"net/http"
	"time"
	"turanga/config"
	"turanga/models"
)

// BaseHandler базовый класс для всех OPDS обработчиков
type BaseHandler struct {
	db  *sql.DB
	cfg *config.Config
}

// NewBaseHandler создает новый экземпляр BaseHandler
func NewBaseHandler(database *sql.DB, cfg *config.Config) *BaseHandler {
	return &BaseHandler{
		db:  database,
		cfg: cfg,
	}
}

// GetDB возвращает базу данных
func (bh *BaseHandler) GetDB() *sql.DB {
	return bh.db
}

// GetPaginationThreshold возвращает порог пагинации из конфигурации
func (bh *BaseHandler) GetPaginationThreshold() int {
	threshold := 60
	if bh.cfg != nil {
		threshold = bh.cfg.PaginationThreshold
	}
	return threshold
}

// CountItems подсчитывает количество элементов по запросу
func (bh *BaseHandler) CountItems(query string, args ...interface{}) (int, error) {
	var count int
	err := bh.db.QueryRow(query, args...).Scan(&count)
	return count, err
}

// RenderNavigationFeed рендерит навигационный фид
func (bh *BaseHandler) RenderNavigationFeed(w http.ResponseWriter, title string, entries []models.Entry) {
	RenderOPDSFeed(w, title, "", entries, false)
}

// RenderAcquisitionFeed рендерит фид с книгами для скачивания
func (bh *BaseHandler) RenderAcquisitionFeed(w http.ResponseWriter, title string, books []*models.Book) {
	var entries []models.Entry
	for _, book := range books {
		entry := CreateAcquisitionEntry(book)
		if entry.Title != "" {
			entries = append(entries, entry)
		}
	}
	RenderOPDSFeed(w, title, "", entries, true)
}

// ShouldPaginate проверяет, нужно ли использовать пагинацию
func (bh *BaseHandler) ShouldPaginate(count int) bool {
	return count > bh.GetPaginationThreshold()
}

// GetBooksByIDs получает книги по ID
func (bh *BaseHandler) GetBooksByIDs(bookIDs []int) (map[int]*models.Book, error) {
	return GetBooksByIDs(bh.db, bookIDs)
}

// GetBooksWithAuthors получает книги с авторами
func (bh *BaseHandler) GetBooksWithAuthors(query string, args ...interface{}) (map[int]*models.Book, error) {
	return GetBooksWithAuthors(bh.db, query, args...)
}

// SortBooksByTitle сортирует книги по названию
func (bh *BaseHandler) SortBooksByTitle(booksMap map[int]*models.Book) []*models.Book {
	return SortBooksByTitle(booksMap)
}

// SortBooksByID сортирует книги по ID
func (bh *BaseHandler) SortBooksByID(booksMap map[int]*models.Book) []*models.Book {
	return SortBooksByID(booksMap)
}

// CreatePlaceholders создает плейсхолдеры
func (bh *BaseHandler) CreatePlaceholders(n int) string {
	return CreatePlaceholders(n)
}

// ConvertToInterfaceSlice конвертирует slice
func (bh *BaseHandler) ConvertToInterfaceSlice(slice []int) []interface{} {
	return ConvertToInterfaceSlice(slice)
}

// GetCurrentTime возвращает текущее время в формате OPDS
func (bh *BaseHandler) GetCurrentTime() string {
	return time.Now().Format("2006-01-02T15:04:05+00:00")
}
