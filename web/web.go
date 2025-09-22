// web/web.go
package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"turanga/config"
	"turanga/nostr"
	"turanga/scanner"
)

// Максимальный размер загружаемой книги — 100MB
const maxFileSize = 100 << 20

// WebInterface представляет веб-интерфейс приложения
type WebInterface struct {
	db              *sql.DB
	config          *config.Config
	NostrClient     *nostr.Client
	appTitle        string
	rootPath        string
	templateCache   *template.Template
	templateOnce    sync.Once
	coverCache      sync.Map // map[string]string
	annotationCache sync.Map // map[string]string
}

// NewWebInterface создает новый экземпляр WebInterface
func NewWebInterface(database *sql.DB, cfg *config.Config, nc *nostr.Client, rootPath string) *WebInterface {
	return &WebInterface{
		db:          database,
		config:      cfg,
		NostrClient: nc,
		appTitle:    "v0.1",
		rootPath:    rootPath,
	}
}

// isAuthenticated проверяет, авторизован ли пользователь
func (w *WebInterface) isAuthenticated(r *http.Request) bool {
	cookie, err := r.Cookie("auth")
	if err != nil {
		return false
	}

	// Если хэш не установлен, разрешаем доступ (первый вход)
	if w.config.PasswordHash == "" {
		return true
	}

	return cookie.Value == w.config.PasswordHash
}

// loadTemplates загружает все шаблоны из файлов
func (w *WebInterface) loadTemplates() (*template.Template, error) {
	var err error
	w.templateOnce.Do(func() {
		// Загружаем базовый шаблон и все остальные
		tmpl := template.New("").Funcs(template.FuncMap{
			"sub":        func(a, b int) int { return a - b },
			"split":      strings.Split,
			"trim":       strings.TrimSpace,
			"urlquery":   url.QueryEscape,
			"upper":      strings.ToUpper,
			"formatSize": FormatFileSize,
		})

		templateFiles := []string{
			filepath.Join(w.rootPath, "web", "templates", "catalog.html"),
			filepath.Join(w.rootPath, "web", "templates", "auth.html"),
			filepath.Join(w.rootPath, "web", "templates", "upload.html"),
			filepath.Join(w.rootPath, "web", "templates", "author.html"),
			filepath.Join(w.rootPath, "web", "templates", "book_detail.html"),
			filepath.Join(w.rootPath, "web", "templates", "series.html"),
			filepath.Join(w.rootPath, "web", "templates", "tag.html"),
			filepath.Join(w.rootPath, "web", "templates", "request.html"),
		}

		w.templateCache, err = tmpl.ParseFiles(templateFiles...)
	})

	return w.templateCache, err
}

// getAnnotationFromFile для чтения аннотации из файла
func (w *WebInterface) getAnnotationFromFile(bookID int, fileHash string) string {
	// ВСЕГДА ищем аннотации в каталоге 'notes' рядом с исполняемым файлом
	var notesDir string
	if w.rootPath != "" {
		notesDir = filepath.Join(w.rootPath, "notes")
	} else {
		notesDir = "./notes"
	}

	// Формируем путь к файлу аннотации
	noteFileName := fmt.Sprintf("%s.txt", fileHash)
	noteFilePath := filepath.Join(notesDir, noteFileName)

	// Читаем аннотацию из файла
	content, err := os.ReadFile(noteFilePath)
	if err != nil {
		// Файл не найден или ошибка чтения
		log.Printf("Аннотация не найдена или ошибка чтения %s: %v", noteFilePath, err) // Можно добавить лог для отладки
		return ""
	}

	return string(content) // Явно конвертируем []byte в string
}

// getCoverURLFromFileHash для получения пути к обложке
func (w *WebInterface) getCoverURLFromFileHash(fileHash string) string {
	if fileHash == "" {
		return ""
	}

	// Всегда ищем обложки в каталоге 'covers' рядом с исполняемым файлом
	var coversDir string
	if w.rootPath != "" {
		coversDir = filepath.Join(w.rootPath, "covers")
	} else {
		coversDir = "./covers"
	}

	// Проверяем существование каталога
	if _, err := os.Stat(coversDir); os.IsNotExist(err) {
		// Каталог covers не существует, обложки там точно нет
		// Можно попытаться создать, если это уместно, или просто вернуть ""
		// os.MkdirAll(coversDir, 0755)
		return ""
	} else if err != nil {
		// Другая ошибка доступа
		log.Printf("Ошибка проверки каталога обложек %s: %v", coversDir, err)
		return ""
	}

	// Возможные расширения обложек
	extensions := []string{".jpg", ".jpeg", ".png", ".gif", ".webp"}
	// Ищем файл обложки с любым из расширений
	for _, ext := range extensions {
		coverFileName := fileHash + ext
		coverFilePath := filepath.Join(coversDir, coverFileName)
		if _, err := os.Stat(coverFilePath); err == nil {
			// Файл существует, возвращаем URL относительно корня веб-сервера
			return "/covers/" + coverFileName
		}
	}
	// Файл с подходящим именем и расширением не найден
	return ""
}

// RequestBookViaNostrHandler обрабатывает запрос книги через Nostr
func (w *WebInterface) RequestBookViaNostrHandler(wr http.ResponseWriter, r *http.Request) {
	// Проверяем аутентификацию
	if !w.isAuthenticated(r) {
		http.Error(wr, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Принимаем только POST запросы
	if r.Method != http.MethodPost {
		http.Error(wr, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Парсим данные формы
	author := strings.TrimSpace(r.FormValue("author"))
	series := strings.TrimSpace(r.FormValue("series"))
	title := strings.TrimSpace(r.FormValue("title"))
	fileHash := strings.TrimSpace(r.FormValue("file_hash"))

	// Базовая валидация (можно расширить)
	if title == "" {
		http.Error(wr, "Название книги обязательно", http.StatusBadRequest)
		return
	}

	// Создаем контекст с таймаутом
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	// Публикуем событие запроса через Nostr клиент
	err := w.NostrClient.PublishBookRequestEvent(ctx, author, series, title, fileHash)
	if err != nil {
		log.Printf("Ошибка публикации запроса книги через Nostr: %v", err)
		http.Error(wr, "Ошибка отправки запроса в сеть Nostr: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Отправляем успешный ответ
	wr.Header().Set("Content-Type", "application/json")
	wr.WriteHeader(http.StatusOK)
	json.NewEncoder(wr).Encode(map[string]string{"status": "success", "message": "Запрос успешно отправлен"})
}

// AddToBlacklistHandler обрабатывает добавление записей в чёрный список
func (w *WebInterface) AddToBlacklistHandler(wr http.ResponseWriter, r *http.Request) {
	// Проверяем аутентификацию
	if !w.isAuthenticated(r) {
		http.Error(wr, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Принимаем только POST запросы
	if r.Method != http.MethodPost {
		http.Error(wr, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Парсим JSON тело запроса
	var requestData struct {
		FileHash string `json:"file_hash"`
		Pubkey   string `json:"pubkey"`
	}

	if err := json.NewDecoder(r.Body).Decode(&requestData); err != nil {
		http.Error(wr, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Проверяем, есть ли Nostr клиент
	if w.NostrClient == nil {
		w.writeJSONError(wr, "Nostr client not initialized", http.StatusInternalServerError)
		return
	}

	// Получаем чёрный список
	blacklist := w.NostrClient.GetBlacklist()
	if blacklist == nil {
		w.writeJSONError(wr, "Blacklist not available", http.StatusInternalServerError)
		return
	}

	// Добавляем записи в чёрный список
	if requestData.FileHash != "" {
		blacklist.AddFileHash(requestData.FileHash)
		log.Printf("file_hash %s в чёрный список", requestData.FileHash)
	}

	if requestData.Pubkey != "" {
		blacklist.AddPubkey(requestData.Pubkey)
		log.Printf("pubkey %s в чёрный список", requestData.Pubkey)
	}

	// Сохраняем чёрный список в файл
	blacklistFile := w.NostrClient.GetBlacklistFile()
	if err := blacklist.SaveToFile(blacklistFile); err != nil {
		log.Printf("Ошибка сохранения чёрного списка: %v", err)
		w.writeJSONError(wr, "Failed to save blacklist: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Отправляем успешный ответ
	wr.Header().Set("Content-Type", "application/json")
	json.NewEncoder(wr).Encode(map[string]interface{}{
		"success": true,
		"message": "Added to blacklist",
	})
}

// Вспомогательная функция для отправки JSON ошибок
func (w *WebInterface) writeJSONError(wr http.ResponseWriter, message string, statusCode int) {
	wr.Header().Set("Content-Type", "application/json")
	wr.WriteHeader(statusCode)
	json.NewEncoder(wr).Encode(map[string]interface{}{
		"success": false,
		"error":   message,
	})
}

// Вспомогательная функция для регистрации скачанной книги
func (w *WebInterface) registerDownloadedBook(filePath string, fileInfo os.FileInfo, title, fileHash string) error {
	// Устанавливаем соединение с БД и конфигурацию для scanner
	scanner.SetDB(w.db)
	if w.config != nil {
		scanner.SetConfig(w.config)
	}

	// Обрабатываем файл как обычную книгу
	err := scanner.ProcessBookFile(filePath, fileInfo)
	if err != nil {
		return fmt.Errorf("failed to process book file: %w", err)
	}

	return nil
}

// Вспомогательная функция для определения расширения по типу файла
func getFileExtensionByType(fileType string) string {
	//	log.Printf("getFileExtensionByType called with: '%s'", fileType)
	fileType = strings.ToLower(fileType)

	switch fileType {
	case "epub":
		return ".epub"
	case "fb2":
		return ".fb2"
	case "fb2.zip", "fb2zip":
		return ".fb2.zip"
	case "pdf":
		return ".pdf"
	case "djvu":
		return ".djvu"
	case "mobi":
		return ".mobi"
	case "txt":
		return ".txt"
	case "html":
		return ".html"
	case "zip":
		return ".zip"
	default:
		// Пытаемся угадать по типу
		if strings.Contains(fileType, "fb2.zip") || strings.Contains(fileType, "fb2zip") {
			return ".fb2.zip"
		}
		if strings.Contains(fileType, "epub") {
			return ".epub"
		}
		if strings.Contains(fileType, "pdf") {
			return ".pdf"
		}
		if strings.Contains(fileType, "djvu") {
			return ".djvu"
		}
		return ""
	}
}

// Вспомогательная функция для отправки JSON ответов
func (w *WebInterface) writeJSONResponse(wr http.ResponseWriter, data interface{}) {
	wr.Header().Set("Content-Type", "application/json")
	json.NewEncoder(wr).Encode(data)
}

// isNostrAvailable проверяет, доступен ли Nostr
func (w *WebInterface) isNostrAvailable() bool {
	cfg := config.GetConfig()

	// Проверяем, инициализирован ли клиент и включен ли он
	if w.NostrClient == nil {
		if cfg.Debug {
			log.Println("Nostr client is nil")
		}
		return false
	}

	isEnabled := w.NostrClient.IsEnabled()
	publicKey := w.NostrClient.GetPublicKey()
	if cfg.Debug {
		log.Printf("Nostr client enabled: %t, public key: %.10s...", isEnabled, publicKey)
	}

	return isEnabled
}

// GetDB возвращает подключение к базе данных
// Публичный метод для доступа к приватному полю db
func (w *WebInterface) GetDB() *sql.DB {
	return w.db
}

// GetCoverURLFromFileHash публичная обертка для приватного метода
func (w *WebInterface) GetCoverURLFromFileHash(fileHash string) string {
	return w.getCoverURLFromFileHash(fileHash)
}

// GetAnnotationFromFile публичная обертка для приватного метода
// Используем bookID int64, как в приватном методе
func (w *WebInterface) GetAnnotationFromFile(bookID int, fileHash string) string {
	return w.getAnnotationFromFile(bookID, fileHash)
}

// InvalidateCoverCache очищает кэш обложек
func (w *WebInterface) InvalidateCoverCache(fileHash string) {
	w.coverCache.Delete(fileHash)
}

// InvalidateAnnotationCache очищает кэш аннотаций
func (w *WebInterface) InvalidateAnnotationCache(bookID int, fileHash string) {
	cacheKey := fmt.Sprintf("%d_%s", bookID, fileHash)
	w.annotationCache.Delete(cacheKey)
}
