// turanga/cmd/smelloscope/main.go
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"turanga/config"

	_ "github.com/mattn/go-sqlite3"
	"github.com/nbd-wtf/go-nostr"
)

const (
	// SubscriptionKind is the Nostr event kind this tool listens for (NIP-8699 responses).
	SubscriptionKind = 8699

	// Reconnect constants
	initialReconnectDelay = 5 * time.Second  // Начальная задержка перед первой попыткой переподключения
	maxReconnectDelay     = 5 * time.Minute  // Максимальная задержка между попытками
	reconnectResetTimeout = 30 * time.Minute // Время без ошибок, после которого задержка сбрасывается
)

// BookResponseData represents the data structure for a book within a Nostr response event (kind 8699).
// This matches the structure defined in `turanga/nostr/subscriber.go`.
type BookResponseData struct {
	ID           int      `json:"id"`                      // Optional ID from responder
	Title        string   `json:"title"`                   // Title of the book
	Authors      []string `json:"authors"`                 // Slice of author names
	Series       string   `json:"series,omitempty"`        // Series name (optional)
	SeriesNumber string   `json:"series_number,omitempty"` // Series number (optional)
	FileType     string   `json:"file_type"`               // Type of the file (e.g., epub, fb2)
	FileHash     string   `json:"file_hash"`               // Hash of the file content
	FileSize     int64    `json:"file_size"`               // Size of the file in bytes
	IPFSCID      string   `json:"ipfs_cid,omitempty"`      // IPFS Content Identifier (optional)
	// Note: raw_data will be the JSON representation of this struct when stored.
}

// BookRecord представляет собой запись книги из базы данных smells.db для отображения.
type BookRecord struct {
	ID            int64
	Title         string
	Authors       string
	Series        sql.NullString
	SeriesNumber  sql.NullString
	FileType      string
	FileHash      sql.NullString
	FileSize      sql.NullInt64
	IPFSCID       sql.NullString
	ReceivedAt    int64  // UNIX timestamp
	ReceivedAtStr string // Форматированная дата
}

// Smelloscope holds the application state.
type Smelloscope struct {
	cfg      *config.Config
	db       *sql.DB
	rootPath string // <-- Добавлено
	// Context for graceful shutdown
	ctx    context.Context
	cancel context.CancelFunc
}

// NewSmelloscope initializes the Smelloscope application.
func NewSmelloscope(configPath, dbPath string) (*Smelloscope, error) {
	// --- Configuration ---
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		// Try loading default config if specific path fails
		log.Printf("Warning: Failed to load config from %s: %v. Trying default config.", configPath, err)
		cfg = config.DefaultConfig()
		// Validate default config
		if validateErr := cfg.Validate(); validateErr != nil {
			return nil, fmt.Errorf("error validating default configuration: %w", validateErr)
		}
		log.Println("Using default configuration.")
	} else {
		log.Printf("Configuration loaded from: %s", configPath)
		if cfg.Debug {
			log.Println("Debug mode is ON.")
		}
	}

	// Ensure global config is set if needed by other turanga packages
	config.SetGlobalConfig(cfg)

	// --- Root Path ---
	exePath, err := os.Executable()
	if err != nil {
		log.Printf("Не удалось определить путь к исполняемому файлу: %v. Использую текущую директорию.", err)
		exePath, _ = os.Getwd()
	}
	rootPath := filepath.Dir(exePath)

	// --- Database ---
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database %s: %w", dbPath, err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping database %s: %w", dbPath, err)
	}
	log.Printf("Database opened: %s", dbPath)

	// Create tables if they don't exist
	if err := createTables(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to create database tables: %w", err)
	}

	// --- Context for Shutdown ---
	ctx, cancel := context.WithCancel(context.Background())

	return &Smelloscope{
		cfg:      cfg,
		db:       db,
		rootPath: rootPath,
		ctx:      ctx,
		cancel:   cancel,
	}, nil
}

// createTables creates the necessary database tables.
func createTables(db *sql.DB) error {
	query := `
	CREATE TABLE IF NOT EXISTS nostr_response_books (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		title TEXT NOT NULL,
		authors TEXT NOT NULL, -- Stored as comma-separated string
		series TEXT,
		series_number TEXT,
		file_type TEXT NOT NULL,
		file_hash TEXT,        -- Not UNIQUE, as one file can have multiple CIDs
		file_size INTEGER,
		ipfs_cid TEXT UNIQUE,  -- Ensures unique IPFS addresses for content
		raw_data TEXT NOT NULL, -- Store the full JSON representation for potential future parsing
		received_at INTEGER NOT NULL -- UNIX timestamp of reception
	);

	-- Indexes for faster lookups
	CREATE INDEX IF NOT EXISTS idx_books_file_hash ON nostr_response_books(file_hash);
	CREATE INDEX IF NOT EXISTS idx_books_ipfs_cid ON nostr_response_books(ipfs_cid);
	`

	_, err := db.Exec(query)
	return err
}

// GetLatestBooks retrieves the latest book records from the database.
func (s *Smelloscope) GetLatestBooks(limit int) ([]BookRecord, error) {
	query := `
        SELECT id, title, authors, series, series_number, file_type, file_hash, file_size, ipfs_cid, received_at
        FROM nostr_response_books
        ORDER BY received_at DESC
        LIMIT ?
    `
	rows, err := s.db.Query(query, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query latest books: %w", err)
	}
	defer rows.Close()

	var books []BookRecord
	for rows.Next() {
		var b BookRecord
		// Используем sql.Null* типы для полей, которые могут быть NULL в БД
		var series, seriesNumber, fileHash, ipfsCID sql.NullString
		var fileSize sql.NullInt64
		err := rows.Scan(&b.ID, &b.Title, &b.Authors, &series, &seriesNumber, &b.FileType, &fileHash, &fileSize, &ipfsCID, &b.ReceivedAt)
		if err != nil {
			// Логируем ошибку, но продолжаем обработку других строк
			log.Printf("Error scanning book row: %v", err)
			continue // Или return nil, err если нужно прервать
		}
		// Присваиваем значения sql.Null* типов обычным типам или используем их как есть
		b.Series = series
		b.SeriesNumber = seriesNumber
		b.FileHash = fileHash
		b.FileSize = fileSize
		b.IPFSCID = ipfsCID
		// Форматируем дату для отображения
		b.ReceivedAtStr = time.Unix(b.ReceivedAt, 0).Format("2006-01-02 15:04:05")

		books = append(books, b)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	return books, nil
}

// Run starts the main loop of the Smelloscope.
func (s *Smelloscope) Run() error {
	defer s.db.Close()

	// --- Graceful Shutdown Setup ---
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		sig := <-sigChan
		log.Printf("Received signal %s, shutting down gracefully...", sig)
		s.cancel() // Cancel the main context
	}()

	// --- Independent Nostr Subscription Loop ---
	// Get relays from config (same logic as in subscriber.go)
	relays := []string{"wss://relay.primal.net"}
	if s.cfg != nil && s.cfg.NostrRelays != "" {
		relays = strings.Split(s.cfg.NostrRelays, ",")
		for i, r := range relays {
			relays[i] = strings.TrimSpace(r)
		}
	}
	log.Printf("Connecting to Nostr relays for kind %d: %v", SubscriptionKind, relays)

	// --- Main Event Processing Loop ---

	// Переменные для управления переподключением
	var lastErrorTime time.Time
	currentReconnectDelay := initialReconnectDelay
	var subCtx context.Context
	var subCancel context.CancelFunc
	var eventChan chan *nostr.Event
	var errChan chan error
	var activeRelayCount int

	// Функция для запуска подписок
	startSubscriptions := func() {
		log.Println("Attempting to connect to relays...")
		// Отменяем предыдущий контекст, если он существует
		if subCancel != nil {
			log.Println("Cancelling previous subscription context...")
			subCancel()
		}
		// Создаем новый контекст для новых подписок
		subCtx, subCancel = context.WithCancel(s.ctx)

		// Переинициализируем каналы
		eventChan = make(chan *nostr.Event, 100)
		errChan = make(chan error, len(relays))

		// Запускаем горутины для каждого релея
		activeRelayCount = 0 // Сбросим счетчик перед запуском
		for _, relayURL := range relays {
			go s.subscribeToRelay(subCtx, relayURL, eventChan, errChan)
			activeRelayCount++
		}
		log.Printf("Subscription attempt initiated. Target active relay count: %d", activeRelayCount)
	}

	// Инициализируем первую попытку подключения
	startSubscriptions()

	running := true
	for running {
		select {
		case <-s.ctx.Done():
			log.Println("Main context cancelled, stopping event loop.")
			// Отменяем контекст подписок при завершении
			if subCancel != nil {
				subCancel()
			}
			running = false
		case err := <-errChan:
			now := time.Now()
			log.Printf("Error from relay subscription: %v", err)

			// Проверяем, прошло ли достаточно времени с последней ошибки для сброса задержки
			if now.Sub(lastErrorTime) > reconnectResetTimeout {
				currentReconnectDelay = initialReconnectDelay
				if s.cfg.Debug {
					log.Printf("Resetting reconnect delay to %v after timeout.", currentReconnectDelay)
				}
			}
			lastErrorTime = now

			// Уменьшаем счетчик активных релеев
			activeRelayCount--
			if s.cfg.Debug {
				log.Printf("Active relays count: %d", activeRelayCount)
			}

			// Если все релеи "потеряны"
			if activeRelayCount <= 0 {
				log.Printf("All relay connections lost. Will attempt to reconnect in %v...", currentReconnectDelay)

				// Ждем указанное время или до отмены контекста
				select {
				case <-time.After(currentReconnectDelay):
					// Перезапускаем подписки
					startSubscriptions()

					// Увеличиваем задержку для следующего возможного переподключения
					currentReconnectDelay *= 2
					if currentReconnectDelay > maxReconnectDelay {
						currentReconnectDelay = maxReconnectDelay
					}
					if s.cfg.Debug {
						log.Printf("Next reconnect delay will be %v if needed.", currentReconnectDelay)
					}

				case <-s.ctx.Done():
					log.Println("Context cancelled while waiting to reconnect.")
					// Отменяем контекст подписок при завершении
					if subCancel != nil {
						subCancel()
					}
					running = false
				}
			}

		case event := <-eventChan:
			if event == nil {
				continue
			}
			if event.Kind != SubscriptionKind {
				continue
			}
			if s.cfg.Debug {
				log.Printf("Received event ID: %s, Kind: %d, CreatedAt: %d", event.ID, event.Kind, event.CreatedAt)
			}
			if err := s.processEvent(event); err != nil {
				log.Printf("Error processing event %s: %v", event.ID, err)
			}
			// Сбросим таймер ошибок при успешной обработке события
			// Это означает, что соединение стабильно
			lastErrorTime = time.Time{}                   // Сброс времени последней ошибки
			currentReconnectDelay = initialReconnectDelay // Сброс задержки

		case <-time.After(60 * time.Second): // Периодический keep-alive log
			if s.cfg.Debug {
				log.Printf("Still listening for Nostr events (kind %d)... Active relays: %d", SubscriptionKind, activeRelayCount)
			}
			// Также сбросим таймер ошибок, если давно нет ошибок и активны релеи
			if activeRelayCount > 0 && time.Since(lastErrorTime) > reconnectResetTimeout {
				lastErrorTime = time.Time{}
				currentReconnectDelay = initialReconnectDelay
				if s.cfg.Debug {
					log.Println("Periodic reset of reconnect delay due to prolonged stability.")
				}
			}
		}
	}

	// Убедимся, что контекст подписок отменен при выходе
	if subCancel != nil {
		subCancel()
	}

	return nil
}

// subscribeToRelay connects to a single Nostr relay and subscribes to kind 8699 events.
// It runs in its own goroutine and sends received events or errors to the provided channels.
func (s *Smelloscope) subscribeToRelay(ctx context.Context, relayURL string, eventChan chan<- *nostr.Event, errChan chan<- error) {
	cfg := config.GetConfig() // Use global config for debug logs if needed inside this func
	defer func() {
		if r := recover(); r != nil {
			log.Printf("Panic in relay subscription goroutine for %s: %v", relayURL, r)
			errChan <- fmt.Errorf("panic in subscription goroutine for %s: %v", relayURL, r)
		}
	}()

	// Parameters for retries
	retryDelay := 1 * time.Second
	maxRetryDelay := 30 * time.Second

	// Main connection loop with retries
	for {
		select {
		case <-ctx.Done():
			log.Printf("Context cancelled, stopping subscription goroutine for relay %s", relayURL)
			return
		default:
		}

		// Connect to the relay with a timeout
		connectCtx, cancelConnect := context.WithTimeout(ctx, 10*time.Second)
		relay, err := nostr.RelayConnect(connectCtx, relayURL)
		cancelConnect()

		if err != nil {
			log.Printf("Failed to connect to relay %s: %v", relayURL, err)
			errChan <- fmt.Errorf("failed to connect to relay %s: %w", relayURL, err)

			// Wait before retrying, or check for context cancellation
			select {
			case <-ctx.Done():
				log.Printf("Context cancelled during retry wait for %s", relayURL)
				return
			case <-time.After(retryDelay):
				// Exponential backoff
				retryDelay *= 2
				if retryDelay > maxRetryDelay {
					retryDelay = maxRetryDelay
				}
				continue // Retry connection
			}
		}

		// Reset retry delay on successful connection
		retryDelay = 1 * time.Second
		if cfg.Debug {
			log.Printf("Connected to relay: %s", relayURL)
		}

		// Create subscription filter for kind 8699
		// Consider a small time window to avoid fetching very old events on startup
		sinceTime := time.Now().Add(-1 * time.Hour) // Last hour
		sinceTimestamp := nostr.Timestamp(sinceTime.Unix())

		filter := nostr.Filter{
			Kinds: []int{SubscriptionKind}, // Only kind 8699
			Since: &sinceTimestamp,
		}

		// Subscribe to events
		sub, err := relay.Subscribe(ctx, []nostr.Filter{filter})
		if err != nil {
			log.Printf("Failed to subscribe on relay %s: %v", relayURL, err)
			relay.Close()
			// Treat subscription failure as a connection issue and retry
			errChan <- fmt.Errorf("failed to subscribe on relay %s: %w", relayURL, err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(retryDelay):
				retryDelay *= 2
				if retryDelay > maxRetryDelay {
					retryDelay = maxRetryDelay
				}
				continue
			}
		}

		log.Printf("Subscribed to kind %d on relay: %s", SubscriptionKind, relayURL)

		// Event reception loop for this relay
		for {
			select {
			case <-ctx.Done():
				log.Printf("Context cancelled, unsubscribing and closing relay %s", relayURL)
				sub.Unsub()
				relay.Close()
				return
			case event := <-sub.Events:
				if event == nil {
					// Channel closed or other issue
					log.Printf("Event channel closed for relay %s, reconnecting...", relayURL)
					sub.Unsub()
					relay.Close()
					// Trigger a reconnect by breaking out of the event loop
					errChan <- fmt.Errorf("event channel closed for relay %s", relayURL)
					select {
					case <-ctx.Done():
						return
					case <-time.After(retryDelay):
						retryDelay *= 2
						if retryDelay > maxRetryDelay {
							retryDelay = maxRetryDelay
						}
						break // Breaks the inner event loop, goes to outer connection loop
					}
					break // Explicit break from switch, though the select above handles it
				}
				// Send the event to the main processing loop
				// Use a select with default to prevent blocking if the main loop is slow
				select {
				case eventChan <- event:
					// Event sent successfully
				case <-ctx.Done():
					sub.Unsub()
					relay.Close()
					return
				default:
					// Channel is full, log and drop event (or implement backpressure)
					log.Printf("Warning: Event channel is full, dropping event %s from %s", event.ID, relayURL)
				}
			}
		}
		// This point should not be reached due to the infinite loops above.
		// The function returns only via context cancellation or explicit breaks/returns.
	}
}

// processEvent handles a single incoming Nostr event (expected to be kind 8699).
// This logic is adapted from `nostr/events.go` -> `handleIncomingBookResponse`.
func (s *Smelloscope) processEvent(event *nostr.Event) error {
	cfg := config.GetConfig() // Get global config for debug logs

	if cfg.Debug {
		log.Printf("Processing incoming book response event (ID: %s) from pubkey: %s", event.ID, event.PubKey)
	}

	// 1. Parse the event content as JSON array of BookResponseData
	var responseData []BookResponseData
	if err := json.Unmarshal([]byte(event.Content), &responseData); err != nil {
		// Log the problematic content for debugging
		log.Printf("Warning: Failed to unmarshal event content (ID: %s) as JSON array: %v. Content snippet: %.100s...", event.ID, err, event.Content)

		// Try parsing as a single object if the array format fails (fallback)
		var singleResponse BookResponseData
		if singleErr := json.Unmarshal([]byte(event.Content), &singleResponse); singleErr != nil {
			log.Printf("Warning: Failed to unmarshal event content (ID: %s) as single JSON object either: %v", event.ID, singleErr)
			return fmt.Errorf("content is not valid JSON for book response(s): %w", err) // Return the original array error
		}
		// If single object parsing succeeded, treat it as a slice with one element
		responseData = append(responseData, singleResponse)
		if cfg.Debug {
			log.Printf("Info: Parsed event %s content as a single book response object.", event.ID)
		}
	}

	// 2. Extract information about the original request from event tags (NIP-10)
	var requestEventID string
	// Extract tags (simplified version of logic in handleIncomingBookResponse)
	for _, tag := range event.Tags {
		if len(tag) >= 2 && tag[0] == "e" {
			// Look for "reply" marker first (NIP-10)
			if len(tag) >= 4 && tag[3] == "reply" {
				requestEventID = tag[1]
				break // Prefer the explicit reply marker
			} else if requestEventID == "" { // Fallback to first 'e' tag if no marker
				requestEventID = tag[1]
			}
		}
	}

	if requestEventID == "" && cfg.Debug {
		log.Printf("Warning: Could not extract original request ID from tags of event %s", event.ID)
	}

	if cfg.Debug {
		log.Printf("Event %s details: Found %d books, in reply to request: %s", event.ID, len(responseData), requestEventID)
	}

	// 3. Basic validation and saving loop
	storedCount := 0
	for _, book := range responseData {
		// Basic validation: Skip if essential data is missing
		if book.Title == "" || len(book.Authors) == 0 || book.FileType == "" {
			if cfg.Debug {
				log.Printf("Skipping book response with missing essential data (Title, Authors, or FileType) in event %s. Title: '%s'", event.ID, book.Title)
			}
			continue
		}

		// Store the book in the smelloscope database
		if err := s.storeBookResponse(book); err != nil {
			// Log the error but continue processing other books in the same event
			log.Printf("Error storing book response (Title: %s) from event %s: %v", book.Title, event.ID, err)
			continue // Don't return error here, process other books
		}
		storedCount++
		if cfg.Debug {
			// Truncate long fields for logging
			titleLog := book.Title
			if len(titleLog) > 50 {
				titleLog = titleLog[:47] + "..."
			}
			authorsLog := strings.Join(book.Authors, ", ")
			if len(authorsLog) > 50 {
				authorsLog = authorsLog[:47] + "..."
			}
			log.Printf("Stored book from event %s: '%s' by [%s] (Hash: %s, CID: %s)", event.ID, titleLog, authorsLog, book.FileHash, book.IPFSCID)
		}
	}

	if storedCount > 0 {
		log.Printf("Processed event %s: Stored %d book(s)", event.ID, storedCount)
	} else if cfg.Debug {
		log.Printf("Processed event %s: No valid books stored", event.ID)
	}

	return nil
}

// storeBookResponse saves a single book response to the smelloscope database.
// This logic is adapted from the saving part of `nostr/events.go` -> `handleIncomingBookResponse`.
func (s *Smelloscope) storeBookResponse(book BookResponseData) error {
	// Serialize the book data back to JSON for raw_data storage
	// This ensures we capture the exact data received, even if our struct evolves.
	rawData, err := json.Marshal(book)
	if err != nil {
		return fmt.Errorf("failed to marshal book data for raw_data storage: %w", err)
	}

	// Prepare the SQL statement for upsert (INSERT OR REPLACE)
	// This handles both new entries and updates if a UNIQUE constraint is hit (e.g., same ipfs_cid)
	// Note: If a UNIQUE constraint is hit, the existing row is deleted and the new one is inserted.
	// The `received_at` timestamp will be updated to the current time.
	stmt, err := s.db.Prepare(`
		INSERT OR REPLACE INTO nostr_response_books (
			title, authors, series, series_number, file_type, file_hash, file_size, ipfs_cid, raw_data, received_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare SQL statement: %w", err)
	}
	defer stmt.Close()

	// Convert Authors slice to a comma-separated string for storage
	authorsStr := strings.Join(book.Authors, ", ")

	receivedAt := time.Now().Unix()

	_, err = stmt.Exec(
		book.Title,
		authorsStr, // Store as string
		book.Series,
		book.SeriesNumber,
		book.FileType,
		book.FileHash, // Not UNIQUE
		book.FileSize,
		book.IPFSCID,    // UNIQUE constraint
		string(rawData), // Store the JSON representation
		receivedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to execute SQL statement: %w", err)
	}

	// Note on behavior with INSERT OR REPLACE and UNIQUE constraints:
	// If a book with the same `ipfs_cid` already exists:
	// 1. The existing row is removed.
	// 2. A new row is inserted with the data from this event.
	// 3. The `received_at` timestamp is updated to the current time.
	// 4. Other fields (title, authors, file_hash etc.) are updated to the values from this event.
	// This means the database always reflects the *last seen* data for a unique IPFS CID.
	// Multiple entries with the same `file_hash` but different `ipfs_cid` are allowed.

	return nil
}

// Close cleans up resources.
func (s *Smelloscope) Close() error {
	s.cancel() // Ensure context is cancelled
	// Closing db is handled by defer in Run()
	// Closing Nostr connections is handled by context cancellation and defer in subscribeToRelay
	return nil
}

const bookListTemplate = `
<!DOCTYPE html>
<html>
<head>
    <title>Smelloscope - Latest Books</title>
    <meta charset="utf-8">
	<link rel="icon" type="image/png" sizes="32x32" href="/static/smsc.png">
    <style>
        body { 
            font-family: Arial, sans-serif; 
            margin: 20px; 
            background-image: url('/static/sky.gif');
            background-repeat: repeat;
            color: #d3d3d3; /* светло-светло-серый */
        }
        table { 
            border-collapse: collapse; 
            width: 100%; 
            background-color: rgba(255, 255, 255, 0.05); /* полупрозрачный фон для таблицы */
        }
        th, td { 
            border: 1px solid #ddd; 
            padding: 8px; 
            text-align: left; 
        }
        th { 
            background-color: #121212; 
        }
        tr:nth-child(even) { 
            background-color: rgba(255, 255, 255, 0.1); 
        }
        .cid-link { 
            color: #a9a9a9; /* светло-серый */
            text-decoration: none; 
            font-weight: bold;
        }
        .cid-link:hover { 
            text-decoration: none; /* убран underline при наведении */
        }
        .size { 
            text-align: right; 
        } /* Выравнивание размера по правому краю */
        h1 {
            color: #d3d3d3; /* светло-светло-серый заголовок */
        }
    </style>
</head>
<body>
    <h1>Latest 100 Books from Smelloscope</h1>
    <table>
        <thead>
            <tr>
                <th>Received At</th>
                <th>Title</th>
                <th>Authors</th>
                <th>Series</th>
                <th>Type</th>
                <th>Size</th>
                <th>xxhash→IPFS CID</th>
            </tr>
        </thead>
        <tbody>
            {{range .Books}}
            <tr>
                <td>{{.ReceivedAtStr}}</td>
                <td>{{.Title}}</td>
                <td>{{.Authors}}</td>
                <td>{{if .Series.Valid}}{{.Series.String}}{{if .SeriesNumber.Valid}} #{{.SeriesNumber.String}}{{end}}{{end}}</td>
                <td>{{.FileType}}</td>
                <td class="size">
                    {{if .FileSize.Valid}}
                        {{.FileSize.Int64}} bytes
                    {{else}}
                        N/A
                    {{end}}
                </td>
                <td>
                    {{if .IPFSCID.Valid}}
                        <a class="cid-link" href="https://dweb.link/ipfs/{{.IPFSCID.String}}" target="_blank">
                            {{if .FileHash.Valid}}{{.FileHash.String}}{{else}}Link{{end}}
                        </a>
                    {{else}}
                        N/A
                    {{end}}
                </td>
            </tr>
            {{else}}
            <tr><td colspan="7">No books found.</td></tr>
            {{end}}
        </tbody>
    </table>
	<p align=right color=777>Turanga v0.2</p>
</body>
</html>
`

func main() {
	var showVersion bool
	var configPath string

	flag.BoolVar(&showVersion, "version", false, "Show version")
	// Default config path is relative to the executable
	flag.StringVar(&configPath, "config", "smelloscope.conf", "Path to config file (relative to executable directory)")
	flag.Parse()

	if showVersion {
		fmt.Println("Smelloscope v0.1.0")
		return
	}

	// Determine paths relative to the executable
	exePath, err := os.Executable()
	if err != nil {
		log.Fatalf("Failed to get executable path: %v", err)
	}
	rootDir := filepath.Dir(exePath)

	// Resolve config and db paths
	if configPath == "" || configPath == "smelloscope.conf" {
		configPath = filepath.Join(rootDir, "smelloscope.conf")
	} else if !filepath.IsAbs(configPath) {
		configPath = filepath.Join(rootDir, configPath)
	}

	dbPath := filepath.Join(rootDir, "smells.db")

	// Setup logging to file and stdout
	logFilePath := filepath.Join(rootDir, "smelloscope.log")
	logFile, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		log.Printf("Failed to open log file %s: %v. Logging to stdout only.", logFilePath, err)
		log.SetOutput(os.Stdout)
	} else {
		// MultiWriter writes to both file and stdout
		multiWriter := io.MultiWriter(os.Stdout, logFile)
		log.SetOutput(multiWriter)
		log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds | log.Lshortfile) // Add file:line info
		defer logFile.Close()
	}

	log.Printf("Starting Smelloscope...")
	log.Printf("Executable directory: %s", rootDir)
	log.Printf("Config file path: %s", configPath)
	log.Printf("Database file path: %s", dbPath)
	log.Printf("Log file path: %s", logFilePath)

	// Initialize the application
	app, err := NewSmelloscope(configPath, dbPath)
	if err != nil {
		log.Fatalf("Failed to initialize Smelloscope: %v", err)
	}
	defer app.Close() // Ensure cleanup happens

	// --- HTTP Server Setup ---
	httpServer := &http.Server{
		Addr:    ":8008", // Порт можно сделать настраиваемым через флаг или конфиг
		Handler: nil,     // Обработчики регистрируются ниже
	}

	// Регистрируем обработчик для корневого пути "/"
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Получаем последние 100 книг
		books, err := app.GetLatestBooks(100)
		if err != nil {
			log.Printf("Error fetching latest books: %v", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		// Подготавливаем данные для шаблона
		data := struct {
			Books []BookRecord
		}{
			Books: books,
		}

		// Парсим и выполняем шаблон
		tmpl, err := template.New("books").Parse(bookListTemplate)
		if err != nil {
			log.Printf("Error parsing template: %v", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := tmpl.Execute(w, data); err != nil {
			log.Printf("Error executing template: %v", err)
			// Заголовки уже отправлены, просто логируем
		}
	})

	// --- Добавлено: Регистрируем обработчик для статических файлов ---
	// Предполагаем, что статические файлы лежат в $rootPath/web/static/
	staticDir := filepath.Join(app.rootPath, "web", "static") // <-- Используем app.rootPath
	// Проверяем, существует ли каталог
	if _, err := os.Stat(staticDir); err == nil {
		// Каталог существует, регистрируем обработчик
		http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir(staticDir))))
		log.Printf("Static files served from: %s", staticDir)
	} else {
		// Каталог не найден или другая ошибка
		if os.IsNotExist(err) {
			log.Printf("Static files directory not found: %s. Static files (including favicon) will not be available.", staticDir)
		} else {
			log.Printf("Error checking static files directory %s: %v", staticDir, err)
		}
	}
	// --- Конец добавленного блока ---

	// Запускаем HTTP сервер в отдельной горутине
	go func() {
		log.Printf("Starting HTTP server on %s", httpServer.Addr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("HTTP server error: %v", err)
			// Опционально: вызвать s.cancel() для остановки основного приложения при критической ошибке HTTP
			// s.cancel()
		}
	}()

	// Добавляем остановку HTTP сервера в deferred функцию graceful shutdown
	defer func() {
		log.Println("Shutting down HTTP server...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			log.Printf("HTTP server shutdown error: %v", err)
			httpServer.Close() // Принудительное закрытие
		}
		log.Println("HTTP server stopped.")
	}()
	// --- End HTTP Server Setup ---

	// Run the application
	if err := app.Run(); err != nil {
		log.Fatalf("Smelloscope encountered an error: %v", err)
	}

	log.Println("Smelloscope stopped.")
}
