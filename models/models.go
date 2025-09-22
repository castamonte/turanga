// models/models.go
package models

import (
	//    "database/sql"
	"encoding/xml"
)

type Book struct {
	ID           int        `json:"id"`
	Title        string     `json:"title"`
	Authors      []Author   `json:"authors"`
	Series       string     `json:"series"`
	SeriesNumber string     `json:"series_number"`
	PublishedAt  string     `json:"published_at"`
	Files        []BookFile `json:"files"`
	Annotation   string     `json:"annotation"`
	ISBN         string     `json:"isbn"`
	Year         string     `json:"year"`
	Publisher    string     `json:"publisher"`
	Tags         []string   `json:"tags"`
}

type BookFile struct {
	URL      string `json:"url"`
	Type     string `json:"type"`
	FileHash string `json:"file_hash"`
}

// Author представляет автора книги
// Используем lastName для сортировки/поиска, fullName для отображения
type Author struct {
	LastName string `json:"last_name"`
	FullName string `json:"full_name"`
}

type Series struct {
	Name string `json:"name"`
}

// BookWeb структура для веб-интерфейса
type BookWeb struct {
	ID    int    `json:"id"`
	Title string `json:"title"`
	// Для веб-интерфейса мы будем формировать строку авторов заранее
	// и хранить её в этом поле, так как HTML не работает напрямую с []AuthorWeb
	AuthorsStr   string        `json:"-"` // Не сериализуем в JSON, используем для HTML
	CoverURL     string        `json:"cover_url"`
	Series       string        `json:"series"`
	SeriesNumber string        `json:"series_number"`
	PublishedAt  string        `json:"published_at"`
	Annotation   string        `json:"annotation"`
	ISBN         string        `json:"isbn"`
	Year         string        `json:"year"`
	Publisher    string        `json:"publisher"`
	Files        []BookFileWeb `json:"files"`
	TagsStr      string        `json:"-"`
	FileType     string        `json:"file_type"`
	FileHash     string        `json:"file_hash"`
	FileSize     int64         `json:"file_size"`
	Over18       bool          `json:"over18"`
	IPFS_CID     string        `json:"ipfs_cid"`
}

// BookFileWeb структура для файла книги в веб-интерфейсе
type BookFileWeb struct {
	URL      string `json:"url"`
	Type     string `json:"type"`
	FileHash string `json:"file_hash"`
	FileSize int64  `json:"file_size"`
}

// Feed представляет собой OPDS каталог
type Feed struct {
	XMLName xml.Name `xml:"http://www.w3.org/2005/Atom feed"`
	Title   string   `xml:"title"`
	ID      string   `xml:"id"`
	Updated string   `xml:"updated"`
	Icon    string   `xml:"icon,omitempty"`
	Links   []Link   `xml:"link"`
	Entries []Entry  `xml:"entry"`
}

// AuthorInfoForOPDS представляет автора для OPDS фида
// Используется внутри Entry
type AuthorInfoForOPDS struct {
	Name string `xml:"name"`
	URI  string `xml:"uri,omitempty"`
}

// Entry представляет собой запись в каталоге (книга или категория)
type Entry struct {
	Title   string              `xml:"title"`
	Updated string              `xml:"updated"`
	ID      string              `xml:"id"`
	Authors []AuthorInfoForOPDS `xml:"author,omitempty"`
	Content Content             `xml:"content"`
	Links   []Link              `xml:"link"`
}

// Content содержит описание записи
type Content struct {
	Type string `xml:"type,attr"`
	Text string `xml:",chardata"`
}

// Link представляет собой ссылку на ресурс
type Link struct {
	Href string `xml:"href,attr"`
	Type string `xml:"type,attr"`
	Rel  string `xml:"rel,attr"`
}

// BookDetailPageData содержит данные для страницы деталей книги
type BookDetailPageData struct {
	Book    *BookWeb     // Используем существующую структуру BookWeb
	Authors []AuthorInfo `json:"authors"` // Добавляем авторов с ID для ссылок
}

// NewFeed создает новый OPDS каталог
func NewFeed(title string) *Feed {
	return &Feed{
		Title:   title,
		ID:      "turanga:catalog",
		Updated: "2025-01-01T00:00:00+00:00",
	}
}

// AuthorInfo структура для информации об авторе с ID (для создания ссылок в веб-интерфейсе)
type AuthorInfo struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}
