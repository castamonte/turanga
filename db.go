// db.go
package main

import (
	"database/sql"
	"log"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"
)

var db *sql.DB

// initDB инициализирует подключение к базе данных
func initDB(rootPath string) {
	var err error
	dbPath := filepath.Join(rootPath, "turanga.db")

	// Используем = вместо := чтобы присвоить значение глобальной переменной
	db, err = sql.Open("sqlite3", dbPath)

	if err != nil {
		log.Fatalf("Ошибка открытия БД %s: %v", dbPath, err)
	}

	// Добавим проверку соединения, чтобы убедиться, что БД открыта
	if err = db.Ping(); err != nil {
		log.Fatalf("Ошибка подключения к БД %s: %v", dbPath, err)
	}

	log.Printf("База данных успешно открыта: %s", dbPath)

	// Создаем таблицы
	_, err = db.Exec(`
        CREATE TABLE IF NOT EXISTS books (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            title TEXT,
            series TEXT,
            series_number TEXT,
            published_at TEXT,
            isbn TEXT,
            year TEXT,
            publisher TEXT,
            file_url TEXT,
            file_type TEXT,
            file_hash TEXT UNIQUE,
            file_size INTEGER,
            over18 INTEGER DEFAULT 0,
            ipfs_cid TEXT UNIQUE
        );
        
        CREATE TABLE IF NOT EXISTS authors (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            last_name TEXT,        -- Для сортировки и поиска
            full_name TEXT UNIQUE  -- Для отображения, UNIQUE для предотвращения дубликатов
        );
        
        CREATE TABLE IF NOT EXISTS book_authors (
            book_id INTEGER,
            author_id INTEGER,
            FOREIGN KEY(book_id) REFERENCES books(id),
            FOREIGN KEY(author_id) REFERENCES authors(id),
            UNIQUE(book_id, author_id)
        );

        CREATE TABLE IF NOT EXISTS tags (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            name TEXT UNIQUE NOT NULL CHECK(LENGTH(name) <= 24)
        );

        CREATE TABLE IF NOT EXISTS book_tags (
            book_id INTEGER,
            tag_id INTEGER,
            FOREIGN KEY(book_id) REFERENCES books(id) ON DELETE CASCADE,
            FOREIGN KEY(tag_id) REFERENCES tags(id) ON DELETE CASCADE,
            UNIQUE(book_id, tag_id)
        );

        -- Таблица для хранения входящих запросов книг через Nostr (kind 8698)
        CREATE TABLE IF NOT EXISTS nostr_book_requests (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            event_id TEXT UNIQUE NOT NULL, -- ID события Nostr (hex)
            pubkey TEXT NOT NULL,           -- Публичный ключ отправителя (hex)
            author TEXT,                    -- Автор из запроса
            series TEXT,                    -- Серия из запроса
            title TEXT,                     -- Название из запроса
            file_hash TEXT,                 -- Хеш файла из запроса
            created_at INTEGER NOT NULL,    -- Время создания события (UNIX timestamp)
            processed BOOLEAN NOT NULL DEFAULT FALSE, -- Флаг обработки
            sent BOOLEAN NOT NULL DEFAULT FALSE,      -- Флаг отправки ответа
            UNIQUE(event_id)                -- Гарантируем уникальность события
        );

        -- Таблица связи между запросами и найденными книгами
        CREATE TABLE IF NOT EXISTS nostr_request_books (
            request_id INTEGER NOT NULL,
            book_id INTEGER NOT NULL,
            file_hash TEXT,
            FOREIGN KEY (request_id) REFERENCES nostr_book_requests (id) ON DELETE CASCADE,
            FOREIGN KEY (book_id) REFERENCES books (id) ON DELETE CASCADE,
            UNIQUE(request_id, book_id) -- Гарантируем уникальность связи
        );

        -- Таблица для хранения ответов, полученных на наши запросы (kind 8699)
        CREATE TABLE IF NOT EXISTS nostr_received_responses (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            event_id TEXT UNIQUE NOT NULL,           -- ID события ответа Nostr (hex)
            responder_pubkey TEXT NOT NULL,          -- Публичный ключ отправителя ответа (hex)
            request_event_id TEXT NOT NULL,          -- ID события запроса, на который дан ответ
            received_at INTEGER NOT NULL,            -- Время получения события (UNIX timestamp)
            content TEXT NOT NULL,                   -- Содержимое события (JSON с массивом BookResponseData)
            processed BOOLEAN NOT NULL DEFAULT FALSE -- Флаг обработки/отображения пользователю
        );

        -- Таблица для хранения данных о книгах из полученных ответов
        CREATE TABLE IF NOT EXISTS nostr_response_books (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            response_id INTEGER NOT NULL,           -- Ссылка на ответ, в котором получена книга
            book_id INTEGER,                        -- ID книги в нашей локальной БД (если найдена)
            title TEXT NOT NULL,                    -- Название книги из ответа
            authors TEXT NOT NULL,                  -- Авторы книги из ответа (строка, разделенная запятыми)
            series TEXT,                            -- Серия из ответа
            series_number TEXT,                     -- Номер в серии из ответа
            file_type TEXT NOT NULL,                -- Тип файла из ответа
            file_hash TEXT,                         -- Хеш файла из ответа
            file_size INTEGER,                      -- Размер файла из ответа
            ipfs_cid TEXT,                          -- IPFS CID из ответа (если есть)
            raw_data TEXT NOT NULL,                 -- Полные необработанные данные книги (JSON BookResponseData)
            FOREIGN KEY (response_id) REFERENCES nostr_received_responses (id) ON DELETE CASCADE
        );

        -- Таблица для хранения друзей (источников книг)
        CREATE TABLE IF NOT EXISTS friends (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            pubkey TEXT UNIQUE NOT NULL,            -- Публичный ключ друга
            name TEXT,                              -- Имя друга (опционально)
            download_count INTEGER DEFAULT 0,       -- Количество скачанных книг от этого друга
            last_download_at INTEGER,               -- Время последнего скачивания
            created_at INTEGER NOT NULL,            -- Время добавления в друзья
            updated_at INTEGER NOT NULL             -- Время последнего обновления
        );

        -- Создаем триггер для автоматического удаления неиспользуемых тегов
        CREATE TRIGGER IF NOT EXISTS delete_unused_tag_after_book_tag_delete
        AFTER DELETE ON book_tags
        FOR EACH ROW
        WHEN NOT EXISTS (SELECT 1 FROM book_tags WHERE tag_id = OLD.tag_id)
        BEGIN
            DELETE FROM tags WHERE id = OLD.tag_id;
        END;

    `)
	if err != nil {
		log.Fatal(err)
	}
}
