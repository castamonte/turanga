package opds

import (
	"turanga/models"
)

// ItemHandler интерфейс для обработчиков элементов
type ItemHandler interface {
	// GetItemsCount возвращает количество элементов
	GetItemsCount() (int, error)
	
	// GetItemsByLetter возвращает элементы на определенную букву
	GetItemsByLetter(letter string) ([]interface{}, error)
	
	// GetAllItems возвращает все элементы
	GetAllItems() ([]interface{}, error)
}

// BookProvider интерфейс для получения книг
type BookProvider interface {
	// GetBooksByIDs возвращает книги по ID
	GetBooksByIDs(ids []int) (map[int]*models.Book, error)
	
	// GetBooksWithAuthors возвращает книги с авторами
	GetBooksWithAuthors(query string, args ...interface{}) (map[int]*models.Book, error)
}
